// SPDX-FileCopyrightText: Copyright The Moby Authors
// SPDX-License-Identifier: Apache-2.0

package sdkapi_test

import (
	"context"
	"net"
	"testing"

	"github.com/moby/extensions/sdk/sdkapi"
	sdkapipb "github.com/moby/extensions/sdk/sdkapi/protogen"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/reflect/protoreflect"
	"gotest.tools/v3/assert"
	is "gotest.tools/v3/assert/cmp"
)

func TestDeclarationOfferWireFields(t *testing.T) {
	t.Parallel()

	declaration := sdkapipb.File_sdk_sdkapi_extension_proto.Messages().ByName("Declaration")
	assert.Assert(t, declaration != nil)

	offered := declaration.Fields().ByName("offered_points")
	assert.Assert(t, offered != nil)
	assert.Equal(t, offered.Number(), protoreflect.FieldNumber(5))
	assert.Equal(t, offered.Cardinality(), protoreflect.Repeated)
	assert.Equal(t, offered.Kind(), protoreflect.StringKind)

	services := declaration.Fields().ByName("provider_services")
	assert.Assert(t, services != nil)
	assert.Equal(t, services.Number(), protoreflect.FieldNumber(6))
	assert.Equal(t, services.Cardinality(), protoreflect.Repeated)
	assert.Equal(t, services.Kind(), protoreflect.MessageKind)

	assert.Assert(t, declaration.Fields().ByName("exposed_services") == nil)
}

type declarationServer struct {
	declaration *sdkapi.Declaration
}

func (s declarationServer) Describe(context.Context, *sdkapi.DescribeRequest) (*sdkapi.DescribeResponse, error) {
	return &sdkapi.DescribeResponse{Declaration: s.declaration}, nil
}

func (declarationServer) Initialize(context.Context, *sdkapi.InitializeRequest) (*sdkapi.InitializeResponse, error) {
	return &sdkapi.InitializeResponse{}, nil
}

func TestDeclarationOfferProtocolRoundTrip(t *testing.T) {
	t.Parallel()

	want := &sdkapi.Declaration{
		ID: "org.example.extension.v1",
		Providers: []sdkapi.PointDeclaration{
			{ID: "org.example.api.v1"},
		},
		OfferedPoints: []string{"org.example.api.v1"},
		ProviderServices: []sdkapi.ProviderServices{
			{Point: "org.example.api.v1", Services: []string{"org.example.api.v1.API"}},
		},
	}

	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	sdkapipb.RegisterServer(server, declarationServer{declaration: want})
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	conn, err := grpc.NewClient("passthrough:///sdkapi",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
	)
	assert.NilError(t, err)
	t.Cleanup(func() { assert.NilError(t, conn.Close()) })

	response, err := sdkapipb.NewClient(conn).Describe(context.Background(), &sdkapi.DescribeRequest{})
	assert.NilError(t, err)
	assert.Assert(t, is.DeepEqual(response.Declaration, want))
}
