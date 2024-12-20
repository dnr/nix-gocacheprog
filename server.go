package main

import (
	"errors"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	// exit server after this much idle time
	idleTime = time.Hour
	// delete files that are this old
	cacheTTL = 60 * 24 * time.Hour
)

func getSystemdSocket() (net.Listener, error) {
	if pid, _ := strconv.Atoi(os.Getenv("LISTEN_PID")); pid != os.Getpid() {
		return nil, errors.New("wrong pid")
	} else if n, _ := strconv.Atoi(os.Getenv("LISTEN_FDS")); n == 0 {
		return nil, errors.New("no fds provided by systemd")
	} else if n > 1 {
		return nil, errors.New("too many fds provided by systemd")
	}
	f := os.NewFile(3, "sock")
	listener, err := net.FileListener(f)
	if err != nil {
		return nil, err
	}
	f.Close()
	return listener, nil
}

func checkIdle(activity chan struct{}, idle time.Duration, fn func()) {
	for {
		select {
		case <-activity:
		case <-time.After(idle):
			fn()
		}
	}
}

func cleanBuildDirs(cacheDir string) {
	ents, err := os.ReadDir(cacheDir)
	if err != nil {
		return
	}
	for _, ent := range ents {
		if strings.HasPrefix(ent.Name(), BuildIDPrefix) {
			os.RemoveAll(filepath.Join(cacheDir, ent.Name()))
		}
	}
}

func serverMain() {
	listener, err := getSystemdSocket()
	if err != nil {
		log.Fatalln("get listen socket:", err)
	}

	cacheDir := os.Getenv("CACHE_DIRECTORY")
	if cacheDir == "" {
		log.Fatalln("systemd didn't set CACHE_DIRECTORY")
	}
	objDir := filepath.Join(cacheDir, "obj")
	os.MkdirAll(objDir, 0755)
	dc := &DiskCache{Dir: objDir}

	exitServer := func() {
		cleanBuildDirs(cacheDir)
		dc.Clean(cacheTTL)
		os.Exit(0)
	}

	activity := make(chan struct{}, 1)
	go checkIdle(activity, idleTime, exitServer)

	// TODO: shut down after a while
	for {
		activity <- struct{}{}
		conn, err := listener.Accept()
		if err != nil {
			log.Println("Accept:", err)
			continue
		}

		var p *Process
		p = &Process{
			In:       conn,
			Out:      conn,
			CacheDir: cacheDir,
			Get:      dc.Get,
			Put:      dc.Put,
			Close: func() error {
				log.Printf("cache: %d gets (%d hits, %d misses, %d errors); %d puts (%d errors)",
					p.Gets.Load(), p.GetHits.Load(), p.GetMisses.Load(), p.GetErrors.Load(), p.Puts.Load(), p.PutErrors.Load())
				return nil
			},
		}
		go func() {
			err := p.Run()
			if err != nil {
				log.Println("run returned", err)
			}
			conn.Close()
			activity <- struct{}{}
		}()
	}
}
