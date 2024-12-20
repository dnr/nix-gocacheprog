package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
)

func main() {
	mode := flag.String("mode", "auto", "which mode to run (client, server, hook, goproxy)")
	flag.Parse()

	if *mode == "auto" {
		*mode = filepath.Base(os.Args[0])
	}

	switch *mode {
	case "client":
		clientMain()
	case "server":
		serverMain()
	case "hook":
		hookMain()
	case "goproxy":
		proxyMain()
	default:
		fmt.Fprintln(os.Stderr, "unknown mode", *mode)
		os.Exit(1)
	}
}
