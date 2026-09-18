// SPDX-FileCopyrightText: Copyright The Moby Authors
// SPDX-License-Identifier: Apache-2.0

// Package singlemsg is a mobyextgen fixture for a single embedded message.
package singlemsg

import (
	"context"

	"github.com/moby/extensions"
)

type Service interface {
	Do(ctx context.Context, req *Request) (*Response, error)
}

type Request struct {
	Nested *Nested `pb:"1"`
}

type Response struct {
	Ok bool `pb:"1"`
}

type Nested struct {
	Value string `pb:"1"`
}

var Point = extensions.DefinePoint[Service]("test.singlemsg.v1")
