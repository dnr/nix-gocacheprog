package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"regexp"
)

const dependsOnUs = `"nativeBuildInputs","[^"]*nix-gocacheprog-hook`

func hookMain() {
	if flag.NArg() < 1 {
		return // not called as a hook properly?
	} else if drv, err := os.ReadFile(flag.Arg(0)); err != nil {
		log.Println("can't open", flag.Arg(0))
		return // just ignore errors
	} else if ok, err := regexp.Match(dependsOnUs, drv); err != nil || !ok {
		return // does not depend on this hook
	}

	socketPath := filepath.Join(SocketDir, SocketFile)
	c, err := net.Dial("unix", socketPath)
	if err != nil {
		log.Fatalln(err)
	}

	br := bufio.NewReader(c)
	jd := json.NewDecoder(br)
	bw := bufio.NewWriter(c)
	je := json.NewEncoder(bw)

	id := genBuildID()
	je.Encode(&Hello{BuildID: id, Phase: PhaseHook})
	bw.Flush()

	var res HookResponse
	if err := jd.Decode(&res); err != nil {
		log.Fatalln(err)
	}

	selfBin, err := os.Readlink("/proc/self/exe")
	if err != nil {
		log.Fatalln(err)
	}

	bw = bufio.NewWriter(os.Stdout)
	fmt.Fprintf(bw, "extra-sandbox-paths\n")
	fmt.Fprintf(bw, "%s\n", SocketDir)
	fmt.Fprintf(bw, "%s/%s=%s\n", SandboxCacheDir, id, res.BuildDir)
	fmt.Fprintf(bw, "%s/client=%s\n", SandboxCacheDir, selfBin)
	bw.Flush()
}
