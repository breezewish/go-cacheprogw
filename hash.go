// Copyright 2017 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Adopted from: https://github.com/golang/go/blob/go1.24.3/src/cmd/go/internal/cache/hash.go

package cacheprogw

import (
	"crypto/sha256"
	"hash"
	"runtime"
	"strings"
)

// An ActionID is a cache action key, the hash of a complete description of a
// repeatable computation (command line, environment variables,
// input file contents, executable contents).
//
// The hash can be generated by Hash.
type ActionID [HashSize]byte

// An OutputID is a cache output key, the hash of an output of a computation.
//
// The hash can be generated by Hash.
type OutputID [HashSize]byte

// HashSize is the number of bytes in a hash.
const HashSize = 32

var hashSalt = stripExperiment(runtime.Version())

// stripExperiment strips any GOEXPERIMENT configuration from the Go
// version string.
func stripExperiment(version string) string {
	if i := strings.Index(version, " X:"); i >= 0 {
		return version[:i]
	}
	return version
}

// A Hash provides access to the canonical hash function used to index the cache.
// The current implementation uses salted SHA256, but clients must not assume this.
type Hash struct {
	h hash.Hash
}

// NewHash returns a new Hash with the salt same to go/cmd (which is runtime.Version).
// The caller is expected to Write data to it and then call Sum.
func NewHash() *Hash {
	return NewHashWithSalt(hashSalt)
}

// NewHashWithSalt returns a new Hash with a custom salt.
// The caller is expected to Write data to it and then call Sum.
func NewHashWithSalt(salt string) *Hash {
	h := &Hash{h: sha256.New()}
	h.Write([]byte(salt))
	return h
}

// Write writes data to the running hash.
func (h *Hash) Write(b []byte) (int, error) {
	return h.h.Write(b)
}

// Sum returns the hash of the data written previously.
func (h *Hash) Sum() [HashSize]byte {
	var out [HashSize]byte
	h.h.Sum(out[:0])
	return out
}
