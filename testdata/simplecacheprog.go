// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package main

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"io"
	"os"
	"time"
)

type Cmd string

const (
	CmdPut   = Cmd("put")
	CmdGet   = Cmd("get")
	CmdClose = Cmd("close")
)

type Request struct {
	ID       int64
	Command  Cmd
	ActionID []byte
	OutputID []byte
	BodySize int64
}

type Response struct {
	ID            int64
	Err           string
	KnownCommands []Cmd
	Miss          bool
	OutputID      []byte
	Size          int64
	Time          *time.Time
	DiskPath      string
}

type CacheEntry struct {
	ActionID []byte
	OutputID []byte
	Time     time.Time
}

var cacheMeta = map[string]CacheEntry{}
var cacheObj = map[string][]byte{}

func nextLine(s *bufio.Scanner) []byte {
	for s.Scan() {
		if len(s.Bytes()) == 0 {
			continue
		}
		return s.Bytes()
	}
	if err := s.Err(); err != nil {
		panic(err)
	}
	panic(io.EOF)
}

func main() {
	writer := json.NewEncoder(os.Stdout)
	reader := bufio.NewScanner(os.Stdin)

	// Initial handshake
	writer.Encode(&Response{
		ID:            0,
		KnownCommands: []Cmd{CmdPut, CmdGet, CmdClose},
	})

	for {
		var req Request
		line := nextLine(reader)
		if err := json.Unmarshal(line, &req); err != nil {
			panic(err)
		}
		if req.Command == CmdPut {
			if req.BodySize > 0 {
				payload := nextLine(reader)
				if payload[0] != '"' || payload[len(payload)-1] != '"' {
					panic("expected base64-encoded body to be quoted")
				}
				body := make([]byte, req.BodySize)
				n, err := base64.StdEncoding.Decode(body, payload[1:len(payload)-1])
				if err != nil {
					panic(err)
				}
				body = body[:n]
				cacheMeta[string(req.ActionID)] = CacheEntry{
					ActionID: req.ActionID,
					OutputID: req.OutputID,
					Time:     time.Now(),
				}
				cacheObj[string(req.OutputID)] = body
			} else {
				cacheMeta[string(req.ActionID)] = CacheEntry{
					ActionID: req.ActionID,
					OutputID: req.OutputID,
					Time:     time.Now(),
				}
				cacheObj[string(req.OutputID)] = nil
			}
			writer.Encode(Response{
				ID:       req.ID,
				OutputID: req.OutputID,
				DiskPath: "/tmp/fakepath",
			})
		} else if req.Command == CmdGet {
			a, ok := cacheMeta[string(req.ActionID)]
			if !ok {
				writer.Encode(&Response{
					ID:   req.ID,
					Miss: true,
				})
				continue
			}
			body := cacheObj[string(a.OutputID)]
			writer.Encode(&Response{
				ID:       req.ID,
				OutputID: a.OutputID,
				Size:     int64(len(body)),
				Time:     &a.Time,
				DiskPath: "/tmp/fakepath",
			})
		} else if req.Command == CmdClose {
			writer.Encode(&Response{ID: req.ID})
			return
		}
	}
}
