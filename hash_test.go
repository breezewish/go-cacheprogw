// Copyright 2017 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cacheprogw

import (
	"fmt"
	"testing"
)

func TestHash(t *testing.T) {
	oldSalt := hashSalt
	hashSalt = ""
	defer func() {
		hashSalt = oldSalt
	}()

	h := NewHash()
	h.Write([]byte("hello world"))
	sum := fmt.Sprintf("%x", h.Sum())
	want := "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9"
	if sum != want {
		t.Errorf("hash(hello world) = %v, want %v", sum, want)
	}
}
