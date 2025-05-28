// Copyright 2017 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

package cacheprogw

import (
	"errors"
	"fmt"
)

// An EntryNotFoundError indicates that a cache entry was not found, with an
// optional underlying reason.
type EntryNotFoundError struct {
	Err error
}

func (e *EntryNotFoundError) Error() string {
	if e.Err == nil {
		return "cache entry not found"
	}
	return fmt.Sprintf("cache entry not found: %v", e.Err)
}

func (e *EntryNotFoundError) Unwrap() error {
	return e.Err
}

var ErrCacheProgClosed = errors.New("cacheprog closed unexpectedly")

var ErrUnsupportedCmd = errors.New("cacheprog does not support command")
