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
	"maps"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
)

// TODO: this is kind of gross but works ok for now
const headerPrefixSize = 4096
const proxyCacheKeyBytes = 24

var skipReturnHeaders = map[string]bool{
	"Alt-Svc":                   true,
	"Content-Transfer-Encoding": true,
	"Transfer-Encoding":         true,
}

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

	var actionID []byte
	// only .mod and .zip paths are immutable and should be cached, others are passed through
	// wihtout caching.
	if strings.HasSuffix(path, ".mod") || strings.HasSuffix(path, ".zip") {
		// make cache key
		hsh := sha256.New()
		fmt.Fprintf(hsh, "gomodproxy v1\n")
		fmt.Fprintf(hsh, "path=%s\n", path)
		fmt.Fprintf(hsh, "headerPrefixSize=%d\n", headerPrefixSize)
		actionID = hsh.Sum(nil)[:proxyCacheKeyBytes]
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
		outReq, err := http.NewRequestWithContext(req.Context(), http.MethodGet, try, nil)
		if err != nil {
			log.Fatal(err)
		}
		res, err := http.DefaultClient.Do(outReq)
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
				copyHeadersReturn(w, res)
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
		copyHeadersReturn(w, res)
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
	cacheRes, err := h.cc.get(actionID)
	if err != nil {
		return err
	}

	if cacheRes.Err != "" {
		return errors.New(cacheRes.Err)
	} else if cacheRes.Miss {
		return errors.New("cache miss")
	} else if cacheRes.DiskPath == "" {
		return errors.New("missing disk path")
	}
	f, err := os.Open(cacheRes.DiskPath)
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

	// verify matching sizes
	if fi, err := f.Stat(); err != nil {
		return err
	} else if cacheRes.Size != fi.Size() {
		return fmt.Errorf("mismatched cache size and disk size: %d != %d", cacheRes.Size, fi.Size())
	} else if cl, err := strconv.Atoi(headers.Get("Content-Length")); err != nil {
		// this should be there, but if not fill it in
		headers.Set("Content-Length", strconv.Itoa(int(cacheRes.Size)))
	} else if cl != int(cacheRes.Size)-headerPrefixSize {
		return fmt.Errorf("cache had wrong Content-Length header: %d != %d", cl, cacheRes.Size-headerPrefixSize)
	}

	// all good, start writing response
	maps.Copy(w.Header(), headers)
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

	// we want to stream through but we need an object id first, so we can't use a hash. use a
	// random id.
	objectID := make([]byte, proxyCacheKeyBytes)
	rand.Read(objectID)

	concat := io.MultiReader(&hbuf, io.TeeReader(res.Body, w))
	if cacheRes, err := h.cc.put(actionID, objectID, int64(hbuf.Len())+res.ContentLength, concat); err != nil {
		return err, false
	} else if cacheRes.Err != "" {
		return errors.New(cacheRes.Err), false
	}
	return nil, false
}

func copyHeadersReturn(w http.ResponseWriter, res *http.Response) {
	for k, vs := range res.Header {
		if !skipReturnHeaders[k] {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}
	}
	w.WriteHeader(res.StatusCode)
}
