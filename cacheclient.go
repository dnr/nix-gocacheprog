package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"sync"
)

type CacheClient struct {
	in    io.Reader
	out   io.Writer
	br    *bufio.Reader
	jd    *json.Decoder
	bw    *bufio.Writer
	je    *json.Encoder
	lock  sync.Mutex
	reqid int64
	inf   map[int64]chan *Response
}

func NewCacheClient(in io.Reader, out io.Writer) *CacheClient {
	br := bufio.NewReader(in)
	jd := json.NewDecoder(br)
	bw := bufio.NewWriter(out)
	je := json.NewEncoder(bw)
	pc := &CacheClient{
		in:    in,
		out:   out,
		br:    br,
		jd:    jd,
		bw:    bw,
		je:    je,
		reqid: 1,
		inf:   make(map[int64]chan *Response),
	}
	go pc.run()
	return pc
}

func (cc *CacheClient) run() error {
	for {
		res := new(Response)
		if err := cc.jd.Decode(res); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}

		cc.lock.Lock()
		resc, ok := cc.inf[res.ID]
		delete(cc.inf, res.ID)
		cc.lock.Unlock()

		if ok {
			resc <- res
		}
	}
}

func (cc *CacheClient) get(actionID []byte) (*Response, error) {
	cc.lock.Lock()

	cc.reqid++
	req := Request{
		ID:       cc.reqid,
		Command:  CmdGet,
		ActionID: actionID,
	}
	if err := cc.je.Encode(req); err != nil {
		cc.lock.Unlock()
		return nil, err
	} else if err = cc.bw.Flush(); err != nil {
		cc.lock.Unlock()
		return nil, err
	}

	ch := make(chan *Response)
	cc.inf[req.ID] = ch

	cc.lock.Unlock()

	res := <-ch
	return res, nil
}

func (cc *CacheClient) put(actionID, objectID, body []byte) (*Response, error) {
	cc.lock.Lock()

	cc.reqid++
	req := Request{
		ID:       cc.reqid,
		Command:  CmdPut,
		ActionID: actionID,
		ObjectID: objectID,
		BodySize: int64(len(body)),
	}
	if err := cc.je.Encode(req); err != nil {
		cc.lock.Unlock()
		return nil, err
	} else if err := cc.je.Encode(body); err != nil {
		cc.lock.Unlock()
		return nil, err
	} else if err = cc.bw.Flush(); err != nil {
		cc.lock.Unlock()
		return nil, err
	}

	ch := make(chan *Response)
	cc.inf[req.ID] = ch

	cc.lock.Unlock()

	res := <-ch
	return res, nil
}
