// SPDX-FileCopyrightText: Copyright The Moby Authors
// SPDX-License-Identifier: Apache-2.0

// Package greeter is the socket-exposure integration-test fixture.
package greeter

import (
	"context"

	"github.com/moby/extensions"
	greeterv0 "github.com/moby/extensions/example/greeter/v0"
	servicev0 "github.com/moby/extensions/extpoints/service/v0"
)

// ID is the extension id and binary name.
const ID = "org.mobyproject.example.greeter.v1"

type greeter struct{}

func (greeter) Greet(_ context.Context, req *greeterv0.HelloRequest) (*greeterv0.HelloReply, error) {
	return &greeterv0.HelloReply{Message: "hello " + req.Name}, nil
}

// Extension publishes the greeter point for socket exposure.
var Extension = extensions.New(extensions.Declaration{
	ID: ID,
	Providers: []extensions.Provider{
		greeterv0.Point.Provide(greeter{}),
		servicev0.Offer(greeterv0.Point),
	},
})
