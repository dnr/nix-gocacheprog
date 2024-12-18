// Copyright 2023 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package cacheproc implements the mechanics of talking to cmd/go's GOCACHE protocol
// so you can write a caching child process at a higher level.
package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
)

// Process implements the cmd/go JSON protocol over stdin & stdout via three
// funcs that callers can optionally implement.
type Process struct {
	In       io.Reader
	Out      io.Writer
	CacheDir string

	// Get optionally specifies a func to look up something from the cache. If
	// nil, all gets are treated as cache misses.touch
	//
	// The actionID is a lowercase hex string of unspecified format or length.
	//
	// The returned outputID must be the same outputID provided to Put earlier;
	// it will be a lowercase hex string of unspecified hash function or length.
	//
	// On cache miss, return all zero values (no error). On cache hit, diskPath
	// must be the absolute path to a regular file; its size and modtime are
	// returned to cmd/go.
	//
	// If the returned diskPath doesn't exist, it's treated as a cache miss.
	Get func(ctx context.Context, actionID string) (outputID, diskPath string, _ error)

	// Put optionally specifies a func to add something to the cache.
	// The actionID and objectID is a lowercase hex string of unspecified format or length.
	// On success, diskPath must be the absolute path to a regular file.
	// If nil, cmd/go may write to disk elsewhere as needed.
	Put func(ctx context.Context, actionID, objectID string, size int64, r io.Reader) (diskPath string, _ error)

	// Close optionally specifies a func to run when the cmd/go tool is
	// shutting down.
	Close func() error

	Gets      atomic.Int64
	GetHits   atomic.Int64
	GetMisses atomic.Int64
	GetErrors atomic.Int64
	Puts      atomic.Int64
	PutErrors atomic.Int64

	buildID  string
	buildDir string
}

func (p *Process) Run() error {
	br := bufio.NewReader(p.In)
	jd := json.NewDecoder(br)

	bw := bufio.NewWriter(p.Out)
	je := json.NewEncoder(bw)

	// --- protocol extension
	var hello Hello
	if err := jd.Decode(&hello); err != nil {
		return err
	}
	res, err := p.setupBuild(&hello)
	if res != nil {
		je.Encode(res)
		bw.Flush()
	}
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil
		}
		return err
	}
	// --- protocol extension

	var caps []Cmd
	if p.Get != nil {
		caps = append(caps, "get")
	}
	if p.Put != nil {
		caps = append(caps, "put")
	}
	if p.Close != nil {
		caps = append(caps, "close")
	}
	je.Encode(&Response{KnownCommands: caps})
	if err := bw.Flush(); err != nil {
		return err
	}

	var wmu sync.Mutex // guards writing responses

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	for {
		var req Request
		if err := jd.Decode(&req); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if req.Command == CmdPut && req.BodySize > 0 {
			// TODO(bradfitz): stream this and pass a checksum-validating
			// io.Reader that validates on EOF.
			var bodyb []byte
			if err := jd.Decode(&bodyb); err != nil {
				log.Fatal(err)
			}
			if int64(len(bodyb)) != req.BodySize {
				log.Fatalf("only got %d bytes of declared %d", len(bodyb), req.BodySize)
			}
			req.Body = bytes.NewReader(bodyb)
		}
		go func() {
			res := &Response{ID: req.ID}
			ctx := ctx // TODO: include req ID as a context.Value for tracing?
			if err := p.handleRequest(ctx, &req, res); err != nil {
				res.Err = err.Error()
			}
			wmu.Lock()
			defer wmu.Unlock()
			je.Encode(res)
			bw.Flush()
		}()
	}
}

func (p *Process) handleRequest(ctx context.Context, req *Request, res *Response) (retErr error) {
	defer func() {
		if retErr == nil {
			retErr = p.linkToBuild(res)
		}
	}()
	switch req.Command {
	default:
		return errors.New("unknown command")
	case "close":
		if p.Close != nil {
			return p.Close()
		}
		return nil
	case "get":
		return p.handleGet(ctx, req, res)
	case "put":
		return p.handlePut(ctx, req, res)
	}
}

func (p *Process) handleGet(ctx context.Context, req *Request, res *Response) (retErr error) {
	p.Gets.Add(1)
	defer func() {
		if retErr != nil {
			p.GetErrors.Add(1)
		} else if res.Miss {
			p.GetMisses.Add(1)
		} else {
			p.GetHits.Add(1)
		}
	}()
	if p.Get == nil {
		res.Miss = true
		return nil
	}
	outputID, diskPath, err := p.Get(ctx, fmt.Sprintf("%x", req.ActionID))
	if err != nil {
		return err
	}
	if outputID == "" && diskPath == "" {
		res.Miss = true
		return nil
	}
	if outputID == "" {
		return errors.New("no outputID")
	}
	res.OutputID, err = hex.DecodeString(outputID)
	if err != nil {
		return fmt.Errorf("invalid OutputID: %v", err)
	}
	fi, err := os.Stat(diskPath)
	if err != nil {
		if os.IsNotExist(err) {
			res.Miss = true
			return nil
		}
		return err
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("not a regular file")
	}
	res.Size = fi.Size()
	res.TimeNanos = fi.ModTime().UnixNano()
	res.DiskPath = diskPath
	return nil
}

func (p *Process) handlePut(ctx context.Context, req *Request, res *Response) (retErr error) {
	actionID, objectID := fmt.Sprintf("%x", req.ActionID), fmt.Sprintf("%x", req.ObjectID)
	p.Puts.Add(1)
	defer func() {
		if retErr != nil {
			p.PutErrors.Add(1)
			log.Printf("put(action %s, obj %s, %v bytes): %v", actionID, objectID, req.BodySize, retErr)
		}
	}()
	if p.Put == nil {
		if req.Body != nil {
			io.Copy(io.Discard, req.Body)
		}
		return nil
	}
	var body io.Reader = req.Body
	if body == nil {
		body = bytes.NewReader(nil)
	}
	diskPath, err := p.Put(ctx, actionID, objectID, req.BodySize, body)
	if err != nil {
		return err
	}
	fi, err := os.Stat(diskPath)
	if err != nil {
		return fmt.Errorf("stat after successful Put: %w", err)
	}
	if fi.Size() != req.BodySize {
		return fmt.Errorf("failed to write file to disk with right size: disk=%v; wanted=%v", fi.Size(), req.BodySize)
	}
	res.DiskPath = diskPath
	return nil
}

// --- protocol extension
func (p *Process) setupBuild(hello *Hello) (*HookResponse, error) {
	if err := validBuildID(hello.BuildID); err != nil {
		return nil, err
	}

	p.buildID = hello.BuildID
	p.buildDir = filepath.Join(p.CacheDir, p.buildID)

	switch hello.Phase {
	case PhaseHook:
		if err := os.MkdirAll(p.buildDir, 0o755); err != nil {
			return nil, err
		}
		return &HookResponse{BuildDir: p.buildDir}, io.EOF
	case PhaseBuild:
		if _, err := os.Stat(p.buildDir); err != nil {
			return nil, fmt.Errorf("unknown build id %s, register with hook first (%w)", p.buildID, err)
		}
		return nil, nil
	default:
		return nil, errors.New("unknown phase in hello command")
	}
}

func (p *Process) linkToBuild(res *Response) error {
	if res.DiskPath == "" {
		return nil
	}
	base := filepath.Base(res.DiskPath)
	inBuild := filepath.Join(p.buildDir, base)
	if err := os.Link(res.DiskPath, inBuild); err != nil && !os.IsExist(err) {
		return err
	}
	res.DiskPath = filepath.Join(SandboxCacheDir, p.buildID, base)
	return nil
}
