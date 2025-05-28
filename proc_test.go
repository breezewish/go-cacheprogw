// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cacheprogw

import (
	"bytes"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

func TestHandshakeTimeout(t *testing.T) {
	proc, err := StartWithConfig("cat", Config{HandshakeTimeout: 1 * time.Second})
	assert.Nil(t, proc)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "timed out")
}

func TestIllegalHandshake(t *testing.T) {
	proc, err := Start(`sh -c 'echo "{\"ID\":0}"'`)
	assert.Nil(t, proc)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no supported commands")
}

func TestHandshakeThenClose(t *testing.T) {
	proc, err := Start(`sh -c 'echo "{\"ID\":0,\"KnownCommands\":[\"get\"]}"; sleep 3'`)
	assert.NotNil(t, proc)
	assert.NoError(t, err)

	err = proc.Close()
	assert.NoError(t, err)
}

func TestHandshakeThenExit(t *testing.T) {
	proc, err := Start(`sh -c 'echo "{\"ID\":0,\"KnownCommands\":[\"get\"]}"'`)
	assert.NotNil(t, proc)
	assert.NoError(t, err)

	// Wait process exit
	time.Sleep(1 * time.Second)

	_, err = proc.Get(ActionID{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "broken pipe")

	err = proc.Close()
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cacheprog closed unexpectedly")
}

func TestGetPut(t *testing.T) {
	proc, err := Start("go run ./testdata/simplecacheprog.go")
	assert.NoError(t, err)
	assert.NotNil(t, proc)

	defer proc.Close()

	val1 := bytes.NewReader([]byte("hello world"))
	key1 := ActionID{}
	copy(key1[:], []byte("actionid0123456789abcdef"))
	putRes, err := proc.Put(key1, val1)
	assert.NoError(t, err)
	assert.Equal(t, int64(len("hello world")), putRes.Size)
	assert.NotEmpty(t, putRes.DiskPath)

	getRes, err := proc.Get(key1)
	assert.NoError(t, err)
	assert.Equal(t, int64(len("hello world")), getRes.Size)
	assert.NotEmpty(t, getRes.DiskPath)

	val2 := bytes.NewReader([]byte("second entry"))
	key2 := ActionID{}
	copy(key2[:], []byte("actionid0123456789abcdee"))
	putRes, err = proc.Put(key2, val2)
	assert.NoError(t, err)
	assert.Equal(t, int64(len("second entry")), putRes.Size)
	assert.NotEmpty(t, putRes.DiskPath)

	getRes, err = proc.Get(key1)
	assert.NoError(t, err)
	assert.Equal(t, int64(len("hello world")), getRes.Size)
	assert.NotEmpty(t, getRes.DiskPath)

	getRes, err = proc.Get(key2)
	assert.NoError(t, err)
	assert.Equal(t, int64(len("second entry")), getRes.Size)
	assert.NotEmpty(t, getRes.DiskPath)

	val3 := bytes.NewReader([]byte("third entry"))
	key3 := ActionID{}
	copy(key3[:], []byte("actionid0123456789abcdec"))
	putRes, err = proc.Put(key3, val3)
	assert.NoError(t, err)
	assert.Equal(t, int64(len("third entry")), putRes.Size)
	assert.NotEmpty(t, putRes.DiskPath)

	getRes, err = proc.Get(key3)
	assert.NoError(t, err)
	assert.Equal(t, int64(len("third entry")), getRes.Size)
	assert.NotEmpty(t, getRes.DiskPath)
}
