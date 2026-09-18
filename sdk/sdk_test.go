// SPDX-FileCopyrightText: Copyright The Moby Authors
// SPDX-License-Identifier: Apache-2.0

package sdk

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/moby/extensions"
	servicev0 "github.com/moby/extensions/extpoints/service/v0"
	"github.com/moby/extensions/sdk/sdkapi"
	sdkapipb "github.com/moby/extensions/sdk/sdkapi/protogen"
	"github.com/moby/extensions/serverpoint"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"gotest.tools/v3/assert"
	is "gotest.tools/v3/assert/cmp"
)

func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp(".", "m") //nolint:usetesting // Keep socket paths relative so they fit Windows' AF_UNIX path limit.
	assert.NilError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func TestRegisterBuildsDeclaration(t *testing.T) {
	srv := NewServer()
	registered := false
	point := serverpoint.Registration{
		Point:    "org.example.point.v1",
		Register: func(grpc.ServiceRegistrar, any) { registered = true },
	}
	ext := extensions.New(extensions.Declaration{
		ID:           "org.example.extension.v1",
		Providers:    []extensions.Provider{{Point: "org.example.point.v1", Impl: struct{}{}}},
		Dependencies: []extensions.Dependency{{Extension: "org.example.dependency"}},
		Conflicts:    []extensions.ExtensionID{"org.example.conflict"},
	})

	err := srv.Register(ext, point)
	assert.NilError(t, err)
	assert.Check(t, registered)

	d := srv.declaration
	assert.Equal(t, d.ID, "org.example.extension.v1")
	assert.Check(t, is.Len(d.Providers, 1))
	assert.Equal(t, d.Providers[0].ID, "org.example.point.v1")
	assert.Check(t, is.Len(d.Dependencies, 1))
	assert.Equal(t, d.Dependencies[0].Extension, "org.example.dependency")
	assert.DeepEqual(t, d.Conflicts, []string{"org.example.conflict"})
}

func TestRegisterRecordsServedServices(t *testing.T) {
	desc := &grpc.ServiceDesc{ServiceName: "org.example.point.v1.Thing", HandlerType: (*any)(nil)}
	served := serverpoint.Registration{
		Point:    "org.example.point.v1",
		Register: func(r grpc.ServiceRegistrar, impl any) { r.RegisterService(desc, impl) },
	}
	srv := NewServer()
	assert.NilError(t, srv.Register(extensions.New(extensions.Declaration{
		ID:        "org.example.extension.v1",
		Providers: []extensions.Provider{{Point: "org.example.point.v1", Impl: struct{}{}}},
	}), served))
	assert.Check(t, is.Len(srv.declaration.ProviderServices, 1))
	assert.Equal(t, srv.declaration.ProviderServices[0].Point, "org.example.point.v1")
	assert.DeepEqual(t, srv.declaration.ProviderServices[0].Services, []string{"org.example.point.v1.Thing"})

	noService := serverpoint.Registration{
		Point:    "org.example.point.v1",
		Register: func(grpc.ServiceRegistrar, any) {},
	}
	plainSrv := NewServer()
	assert.NilError(t, plainSrv.Register(extensions.New(extensions.Declaration{
		ID:        "org.example.extension.v1",
		Providers: []extensions.Provider{{Point: "org.example.point.v1", Impl: struct{}{}}},
	}), noService))
	assert.Check(t, is.Len(plainSrv.declaration.ProviderServices, 1))
	assert.Check(t, is.Len(plainSrv.declaration.ProviderServices[0].Services, 0))
}

func TestRegisterServesOfferedOrdinaryPoint(t *testing.T) {
	fooPoint := extensions.DefinePoint[any]("org.example.foo.v1")
	foo := generatedPointRegistration(fooPoint.ID(), "org.example.services.v1.Foo")
	impl := struct{}{}
	srv := NewServer()
	err := srv.Register(extensions.New(extensions.Declaration{
		ID: "org.example.extension.v1",
		Providers: []extensions.Provider{
			fooPoint.Provide(impl),
			servicev0.Offer(fooPoint),
		},
	}), foo)
	assert.NilError(t, err)
	assert.DeepEqual(t, srv.declaration.OfferedPoints, []string{string(foo.Point)})
	assert.Check(t, is.Len(srv.declaration.Providers, 2))
	assert.Check(t, is.Len(srv.declaration.ProviderServices, 1))
	assert.Equal(t, srv.declaration.ProviderServices[0].Point, string(foo.Point))
	assert.DeepEqual(t, srv.declaration.ProviderServices[0].Services, []string{"org.example.services.v1.Foo"})
	assert.Check(t, is.Len(srv.grpc.GetServiceInfo(), 1))
}

func TestRegisterOffersSubsetOfOrdinaryPoints(t *testing.T) {
	fooPoint := extensions.DefinePoint[any]("org.example.foo.v1")
	barPoint := extensions.DefinePoint[any]("org.example.bar.v1")
	foo := generatedPointRegistration(fooPoint.ID(), "org.example.services.v1.Foo")
	bar := generatedPointRegistration(barPoint.ID(), "org.example.services.v1.Bar")
	srv := NewServer()
	err := srv.Register(extensions.New(extensions.Declaration{
		ID: "org.example.extension.v1",
		Providers: []extensions.Provider{
			fooPoint.Provide(struct{}{}),
			barPoint.Provide(struct{}{}),
			servicev0.Offer(fooPoint),
		},
	}), foo, bar)
	assert.NilError(t, err)
	assert.DeepEqual(t, srv.declaration.OfferedPoints, []string{string(foo.Point)})
	assert.Check(t, is.Len(srv.declaration.ProviderServices, 2))
	assert.Equal(t, srv.declaration.ProviderServices[0].Point, string(foo.Point))
	assert.Equal(t, srv.declaration.ProviderServices[1].Point, string(bar.Point))
	assert.Check(t, is.Len(srv.grpc.GetServiceInfo(), 2))
}

func TestRegisterRejectsServiceNameAttributedToDifferentPoints(t *testing.T) {
	first := generatedPointRegistration("org.example.first.v1", "org.example.services.v1.Shared")
	second := generatedPointRegistration("org.example.second.v1", "org.example.services.v1.Shared")
	srv := NewServer()
	err := srv.Register(extensions.New(extensions.Declaration{
		ID: "org.example.extension.v1",
		Providers: []extensions.Provider{
			{Point: first.Point, Impl: struct{}{}},
			{Point: second.Point, Impl: struct{}{}},
		},
	}), first, second)
	assert.ErrorContains(t, err, `gRPC service "org.example.services.v1.Shared" is attributed to different points`)
	assert.ErrorContains(t, err, `"org.example.first.v1" and "org.example.second.v1"`)
	assert.Check(t, is.Len(srv.grpc.GetServiceInfo(), 1))
}

func TestRegisterRejectsUnimplementedOffer(t *testing.T) {
	point := extensions.DefinePoint[any]("org.example.foo.v1")
	srv := NewServer()
	err := srv.Register(extensions.New(extensions.Declaration{
		ID:        "org.example.extension.v1",
		Providers: []extensions.Provider{servicev0.Offer(point)},
	}))
	assert.ErrorContains(t, err, `offered point "org.example.foo.v1" is not implemented`)
}

func TestRegisterRejectsOfferedPointWithoutService(t *testing.T) {
	point := extensions.DefinePoint[any]("org.example.foo.v1")
	srv := NewServer()
	err := srv.Register(extensions.New(extensions.Declaration{
		ID: "org.example.extension.v1",
		Providers: []extensions.Provider{
			point.Provide(struct{}{}),
			servicev0.Offer(point),
		},
	}), serverpoint.Registration{Point: point.ID(), Register: func(grpc.ServiceRegistrar, any) {}})
	assert.ErrorContains(t, err, `offered point "org.example.foo.v1" registered no gRPC service`)
}

func generatedPointRegistration(point extensions.PointID, service string) serverpoint.Registration {
	desc := &grpc.ServiceDesc{ServiceName: service, HandlerType: (*any)(nil)}
	return serverpoint.Registration{
		Point: point,
		Register: func(r grpc.ServiceRegistrar, impl any) {
			r.RegisterService(desc, impl)
		},
	}
}

func TestRegisterRejectsUnknownPoint(t *testing.T) {
	srv := NewServer()
	ext := extensions.New(extensions.Declaration{
		ID:        "org.example.extension.v1",
		Providers: []extensions.Provider{{Point: "org.example.point.v1", Impl: struct{}{}}},
	})

	err := srv.Register(ext)
	assert.ErrorContains(t, err, "no server registration for point")
}

func TestListenRejectsUnsupportedProtocol(t *testing.T) {
	srv := NewServer()
	err := srv.ListenWithIO(t.Context(), strings.NewReader(`{"endpoint":"/tmp/x.sock","protocolVersion":999}`), io.Discard)
	assert.ErrorContains(t, err, "unsupported extension protocol version")
}

func TestListenDeliversConfig(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	var got extensions.Config
	ext := extensions.New(extensions.Declaration{
		ID: "org.example.extension.v1",
		Init: func(_ context.Context, cfg extensions.Config, _ extensions.Resolver) error {
			got = cfg
			return nil
		},
	})
	srv := NewServer()
	assert.NilError(t, srv.Register(ext))

	endpoint := filepath.Join(shortTempDir(t), "x.sock")
	in, err := json.Marshal(StartupConfig{
		Endpoint:        endpoint,
		ProtocolVersion: ProtocolVersion,
		Config:          extensions.Config{"plugin_path": "/opt/nri", "enabled": true},
	})
	assert.NilError(t, err)

	// Serve in the background and wait for the readiness acknowledgement.
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() { done <- srv.ListenWithIO(ctx, bytes.NewReader(in), pw) }()
	_, err = io.ReadFull(pr, make([]byte, len(ReadinessAck)))
	assert.NilError(t, err)

	conn, err := grpc.NewClient("unix:"+endpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(c context.Context, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(c, "unix", endpoint)
		}))
	assert.NilError(t, err)
	defer func() { assert.NilError(t, conn.Close()) }()

	_, err = sdkapipb.NewClient(conn).Initialize(ctx, &sdkapi.InitializeRequest{})
	assert.NilError(t, err)
	assert.Equal(t, got["plugin_path"], "/opt/nri")
	assert.Equal(t, got["enabled"], true)

	cancel()
	<-done
}
