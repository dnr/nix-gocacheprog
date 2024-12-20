package main

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"strings"
)

// TODO: this is kind of gross but works ok for now
const headerPrefixSize = 4096

type proxyHandler struct {
	cc        *CacheClient
	upstreams []url.URL
}

func proxyMain() {
	log.SetFlags(0)
	log.SetPrefix("nix-gocacheprog mod proxy:")

	// the hook ensures GOPROXY is set here. this GOPROXY does not include ourself.
	var upstreams []url.URL
	for _, up := range strings.Split(os.Getenv("GOPROXY"), ",") {
		if u, err := url.Parse(up); err == nil && u.Scheme == "http" || u.Scheme == "https" {
			upstreams = append(upstreams, *u)
		}
	}
	uc := initClient()
	h := &proxyHandler{
		cc:        NewCacheClient(uc, uc),
		upstreams: upstreams,
	}
	err := http.ListenAndServe(ProxyListen, h)
	if err != nil {
		log.Fatalln(err)
	}
}

func (h *proxyHandler) ServeHTTP(w http.ResponseWriter, req *http.Request) {
	path := req.URL.Path

	// make cache key
	var actionID []byte
	if strings.HasSuffix(path, ".mod") || strings.HasSuffix(path, "zip") {
		hsh := sha256.New()
		fmt.Fprintf(hsh, "gomodproxy v1\n")
		fmt.Fprintf(hsh, "path=%s\n", path)
		fmt.Fprintf(hsh, "headerPrefixSize=%d\n", headerPrefixSize)
		actionID = hsh.Sum(nil)[:24]
	}

	// check if we can get it from cache
	if actionID != nil {
		err := h.getAndWrite(w, actionID)
		if err == nil {
			// log.Printf("hit %s", path)
			return
		}
		// log.Printf("miss %s (%s)", path, err)
	}

	// nope, try upstreams
	for i, up := range h.upstreams {
		islast := i == len(h.upstreams)-1

		try := up.JoinPath(path).String()
		// log.Printf("querying %s", try)
		res, err := http.Get(try)
		if err != nil {
			if islast {
				log.Printf("http error %s on last upstream", err)
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			} else {
				log.Printf("http error %s, trying next", err)
				continue
			}
		}

		if res.StatusCode != http.StatusOK {
			if islast {
				log.Printf("http status %s on last upstream", res.Status)
				// pass through full response
				defer res.Body.Close()
				copyHeaders(w, res)
				io.Copy(w, res.Body)
				return
			} else {
				log.Printf("http status %s, trying next", res.Status)
				res.Body.Close()
				continue
			}
		}

		// we got an ok, let's use this

		defer res.Body.Close()
		copyHeaders(w, res)
		if actionID == nil {
			io.Copy(w, res.Body)
			return
		}

		if err, tryCopyOnError := h.putAndWrite(w, req, res, actionID); err != nil {
			log.Println("put error", err)
			if tryCopyOnError {
				io.Copy(w, res.Body)
			}
			return
		}

		return
	}

	http.Error(w, "no upstreams", http.StatusNotFound)
}

func (h *proxyHandler) getAndWrite(w http.ResponseWriter, actionID []byte) error {
	res, err := h.cc.get(actionID)
	if err != nil {
		return err
	}

	if res.Err != "" {
		return errors.New(res.Err)
	} else if res.Miss {
		return errors.New("cache miss")
	} else if res.DiskPath == "" {
		return errors.New("missing disk path")
	}
	f, err := os.Open(res.DiskPath)
	if err != nil {
		return err
	}
	defer f.Close()

	hbuf := make([]byte, headerPrefixSize)
	if _, err := io.ReadFull(f, hbuf); err != nil {
		return err
	}
	var headers http.Header
	if err := json.Unmarshal(hbuf, &headers); err != nil {
		return err
	}

	// all good, start writing response
	for k, vs := range headers {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(http.StatusOK)
	if _, err := io.Copy(w, f); err != nil {
		log.Println("copy error from cache", err)
	}
	return nil
}

func (h *proxyHandler) putAndWrite(w http.ResponseWriter, req *http.Request, res *http.Response, actionID []byte) (error, bool) {
	if res.ContentLength < 0 {
		return errors.New("can't cache without ContentLength"), true
	}

	var hbuf bytes.Buffer
	hbuf.Grow(headerPrefixSize)
	if err := json.NewEncoder(&hbuf).Encode(res.Header); err != nil {
		return err, true
	} else if hbuf.Len() > headerPrefixSize {
		return errors.New("headers are too big"), true
	}
	for hbuf.Len() < headerPrefixSize {
		hbuf.WriteByte('\n')
	}

	// if we got this far, the client (Go) should accept the response, any errors from here are
	// just our problem.

	// we want to stream through so use a random id. go does its own checksumming.
	objectID := make([]byte, 24)
	rand.Read(objectID)

	concat := io.MultiReader(&hbuf, io.TeeReader(res.Body, w))
	if cacheRes, err := h.cc.put(actionID, objectID, int64(hbuf.Len())+res.ContentLength, concat); err != nil {
		return err, false
	} else if cacheRes.Err != "" {
		return errors.New(cacheRes.Err), false
	}
	return nil, false
}

func copyHeaders(w http.ResponseWriter, res *http.Response) {
	for k, vs := range res.Header {
		// TODO: probably need to filter out hop-by-hop headers
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(res.StatusCode)
}
