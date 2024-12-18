package main

import (
	"errors"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
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

	// TODO: shut down after a while
	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Println("Accept:", err)
			continue
		}
		log.Println("new connection")

		var p *Process
		p = &Process{
			In:       conn,
			Out:      conn,
			CacheDir: cacheDir,
			Get:      dc.Get,
			Put:      dc.Put,
			Close: func() error {
				log.Printf("closed: %d gets (%d hits, %d misses, %d errors); %d puts (%d errors)",
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
		}()
	}
}
