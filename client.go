package main

import (
	"bufio"
	"encoding/json"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
)

func clientMain() {
	// find build id
	dirents, err := os.ReadDir(SandboxCacheDir)
	if err != nil {
		log.Fatalln("readdir", SandboxCacheDir, err)
	}
	var buildID string
	for _, de := range dirents {
		if de.IsDir() && validBuildID(de.Name()) == nil {
			buildID = de.Name()
			break
		}
	}
	if buildID == "" {
		log.Fatalln("can't find build id")
	}

	// connect to server
	socketPath := filepath.Join(SocketDir, SocketFile)
	c, err := net.Dial("unix", socketPath)
	if err != nil {
		log.Fatalln(err)
	}

	// send hello with build id
	bw := bufio.NewWriter(c)
	je := json.NewEncoder(bw)
	je.Encode(&Hello{BuildID: buildID, Phase: PhaseBuild})
	bw.Flush()

	// transfer back and forth
	uc := c.(*net.UnixConn)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		defer uc.CloseWrite()
		if _, err := io.Copy(uc, os.Stdin); err != nil {
			log.Println("copy in error", err)
		}
	}()
	go func() {
		defer wg.Done()
		defer os.Stdout.Close()
		if _, err := io.Copy(os.Stdout, uc); err != nil {
			log.Println("copy out error", err)
		}
	}()
	wg.Wait()
}
