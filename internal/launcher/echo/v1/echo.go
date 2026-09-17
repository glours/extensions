// SPDX-FileCopyrightText: Copyright The Moby Authors
// SPDX-License-Identifier: Apache-2.0

//go:generate go run github.com/moby/extensions/cmd/mobyextgen

// Package echov1 is the minimal extension point used by launcher tests.
package echov1

import (
	"context"

	"github.com/moby/extensions"
)

// Echo is the provider interface for the echo test point.
type Echo interface {
	// Echo returns the request message or an error when it is empty.
	Echo(ctx context.Context, req *EchoRequest) (*EchoResponse, error)
}

// EchoRequest is the echo request.
type EchoRequest struct {
	Message string `pb:"1"`
}

// EchoResponse is the echo response.
type EchoResponse struct {
	Message string `pb:"1"`
}

// Point is the echo test point used by launcher tests.
var Point = extensions.DefinePoint[Echo]("moby.extensions.internal.launcher.echo.v1")
