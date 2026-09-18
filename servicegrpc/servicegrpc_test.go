// SPDX-FileCopyrightText: Copyright The Moby Authors
// SPDX-License-Identifier: Apache-2.0

package servicegrpc

import (
	"context"
	"net"
	"testing"

	greeterv0 "github.com/moby/extensions/example/greeter/v0"
	greeterpb "github.com/moby/extensions/example/greeter/v0/protogen"
	"github.com/moby/extensions/serverpoint"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"gotest.tools/v3/assert"
)

const serviceName = "org.mobyproject.extension.example.greeter.v0.Greeter"

type greeter struct{}

func (greeter) Greet(_ context.Context, req *greeterv0.HelloRequest) (*greeterv0.HelloReply, error) {
	if req.Name == "blocked" {
		return nil, status.Error(codes.PermissionDenied, "blocked")
	}
	return &greeterv0.HelloReply{Message: "hello " + req.Name}, nil
}

func TestAdaptUsesGeneratedGreeterRegistration(t *testing.T) {
	adapted, err := Adapt(greeterpb.ServerPoint, greeter{})
	assert.NilError(t, err)
	assert.Equal(t, adapted.Point, greeterv0.Point.ID())
	assert.Equal(t, adapted.Name, serviceName)
	assert.Equal(t, adapted.Desc.ServiceName, serviceName)
	assert.Equal(t, adapted.Desc.Methods[0].MethodName, "Greet")
	assert.Assert(t, adapted.Impl != nil)
}

func TestGeneratedGreeterPreservesInterceptorFullMethodAndStatus(t *testing.T) {
	intercepted := make(chan string, 2)
	srv := grpc.NewServer(grpc.UnaryInterceptor(func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		intercepted <- info.FullMethod
		return handler(ctx, req)
	}))
	assert.NilError(t, Register(srv, greeterpb.ServerPoint, greeter{}))

	listener := bufconn.Listen(1024 * 1024)
	go func() { _ = srv.Serve(listener) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient("passthrough:///published-services",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
	)
	assert.NilError(t, err)
	t.Cleanup(func() { assert.NilError(t, conn.Close()) })

	client := greeterpb.NewClient(conn)
	resp, err := client.Greet(context.Background(), &greeterv0.HelloRequest{Name: "world"})
	assert.NilError(t, err)
	assert.Equal(t, resp.Message, "hello world")
	assert.Equal(t, <-intercepted, "/"+serviceName+"/Greet")

	_, err = client.Greet(context.Background(), &greeterv0.HelloRequest{Name: "blocked"})
	assert.Equal(t, status.Code(err), codes.PermissionDenied)
	assert.Equal(t, status.Convert(err).Message(), "blocked")
	assert.Equal(t, <-intercepted, "/"+serviceName+"/Greet")
}

func TestAdaptRequiresOneCompleteGeneratedService(t *testing.T) {
	cases := []struct {
		name         string
		registration serverpoint.Registration
		impl         any
		want         string
	}{
		{
			name: "incomplete registration",
			want: "servicegrpc: incomplete service registration",
		},
		{
			name: "no services",
			registration: serverpoint.Registration{
				Point:    "org.example.empty.v1",
				Register: func(grpc.ServiceRegistrar, any) {},
			},
			impl: struct{}{},
			want: `servicegrpc: point "org.example.empty.v1" registered 0 gRPC services; want exactly 1`,
		},
		{
			name: "multiple services",
			registration: serverpoint.Registration{
				Point: "org.example.multiple.v1",
				Register: func(r grpc.ServiceRegistrar, impl any) {
					r.RegisterService(&grpc.ServiceDesc{ServiceName: "example.One"}, impl)
					r.RegisterService(&grpc.ServiceDesc{ServiceName: "example.Two"}, impl)
				},
			},
			impl: struct{}{},
			want: `servicegrpc: point "org.example.multiple.v1" registered 2 gRPC services; want exactly 1`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Adapt(tc.registration, tc.impl)
			assert.Error(t, err, tc.want)
		})
	}
}
