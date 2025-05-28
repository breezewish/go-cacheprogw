// Copyright 2024 The Go Authors. All rights reserved.
// Use of this source code is governed by a BSD-style
// license that can be found in the LICENSE file.

// Package cacheprogw provides utilties to interact with a GOCACHEPROG program
// just like the go command does.
//
// By default, the go command manages a build cache stored in the file system
// itself. GOCACHEPROG can be set to the name of a command (with optional
// space-separated flags) that implements the go command build cache externally.
// This permits defining a different cache policy.
//
// The go command will start the GOCACHEPROG as a subprocess and communicate
// with it via JSON messages over stdin/stdout. The subprocess's stderr will be
// connected to the go command's stderr.
//
// The subprocess should immediately send a [Response] with its capabilities.
// After that, the go command will send a stream of [Request] messages and the
// subprocess should reply to each [Request] with a [Response] message.
package cacheprogw
