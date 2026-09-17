// SPDX-FileCopyrightText: Copyright The Moby Authors
// SPDX-License-Identifier: Apache-2.0

package client

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/moby/extensions"
	"github.com/moby/extensions/clientpoint"
	greeterv0 "github.com/moby/extensions/example/greeter/v0"
	greeterpb "github.com/moby/extensions/example/greeter/v0/protogen"
	echov1 "github.com/moby/extensions/internal/launcher/echo/v1"
	echopb "github.com/moby/extensions/internal/launcher/echo/v1/protogen"
	"google.golang.org/grpc"
	"google.golang.org/grpc/test/bufconn"
	"gotest.tools/v3/assert"
)

const bufconnSize = 1024 * 1024

type testEngine struct {
	dialer func(context.Context) (net.Conn, error)
	dials  atomic.Int64
	conns  chan *countedConn
}

func (e *testEngine) Dialer() func(context.Context) (net.Conn, error) {
	return func(ctx context.Context) (net.Conn, error) {
		e.dials.Add(1)
		return e.dialer(ctx)
	}
}

type nilDialerEngine struct{}

func (nilDialerEngine) Dialer() func(context.Context) (net.Conn, error) { return nil }

type countedConn struct {
	net.Conn
	closes atomic.Int64
}

func (c *countedConn) Close() error {
	c.closes.Add(1)
	return c.Conn.Close()
}

type testGreeter struct{}

func (testGreeter) Greet(_ context.Context, req *greeterv0.HelloRequest) (*greeterv0.HelloReply, error) {
	return &greeterv0.HelloReply{Message: "hello " + req.Name}, nil
}

type testEcho struct{}

func (testEcho) Echo(_ context.Context, req *echov1.EchoRequest) (*echov1.EchoResponse, error) {
	return &echov1.EchoResponse{Message: req.Message}, nil
}

func newTestServer(t *testing.T) *bufconn.Listener {
	t.Helper()

	server := grpc.NewServer()
	greeterpb.ServerPoint.Register(server, testGreeter{})
	echopb.ServerPoint.Register(server, testEcho{})
	listener := bufconn.Listen(bufconnSize)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(func() {
		server.Stop()
		_ = listener.Close()
	})
	return listener
}

func newBufconnEngine(listener *bufconn.Listener) *testEngine {
	engine := &testEngine{conns: make(chan *countedConn, 8)}
	engine.dialer = func(_ context.Context) (net.Conn, error) {
		conn, err := listener.Dial()
		if err != nil {
			return nil, err
		}
		counted := &countedConn{Conn: conn}
		engine.conns <- counted
		return counted, nil
	}
	return engine
}

func validRegistration(point extensions.PointID, provider func(grpc.ClientConnInterface) extensions.Provider) clientpoint.Registration {
	return clientpoint.Registration{Point: point, Provider: provider}
}

func TestNewIsLazyAndResolvesTypedGeneratedClient(t *testing.T) {
	t.Parallel()

	listener := newTestServer(t)
	engine := newBufconnEngine(listener)

	var greeterConn, echoConn grpc.ClientConnInterface
	greeterRegistration := greeterpb.ClientPoint
	greeterRegistration.Single = true
	greeterRegistration.Provider = func(conn grpc.ClientConnInterface) extensions.Provider {
		greeterConn = conn
		return greeterpb.ClientProvider(conn)
	}
	echoRegistration := echopb.ClientPoint
	echoRegistration.Provider = func(conn grpc.ClientConnInterface) extensions.Provider {
		echoConn = conn
		return echopb.ClientProvider(conn)
	}

	client, err := New(engine, WithGRPCPoint(greeterRegistration), WithGRPCPoint(echoRegistration))
	assert.NilError(t, err)
	t.Cleanup(func() { assert.NilError(t, client.Close()) })
	assert.Equal(t, engine.dials.Load(), int64(0), "New must not dial the engine")

	greeter, err := Resolve(client, greeterv0.Point)
	assert.NilError(t, err)
	assert.Equal(t, engine.dials.Load(), int64(0), "resolving a provider must not preflight the transport")

	echo, err := Resolve(client, echov1.Point)
	assert.NilError(t, err)
	assert.Assert(t, greeterConn == echoConn, "all configured points must share one private connection")

	reply, err := greeter.Greet(context.Background(), &greeterv0.HelloRequest{Name: "world"})
	assert.NilError(t, err)
	assert.Equal(t, reply.Message, "hello world")
	response, err := echo.Echo(context.Background(), &echov1.EchoRequest{Message: "round trip"})
	assert.NilError(t, err)
	assert.Equal(t, response.Message, "round trip")
	assert.Equal(t, engine.dials.Load(), int64(1), "the shared lazy connection should dial once")
}

func TestNewFreezesRegistrations(t *testing.T) {
	t.Parallel()

	point := extensions.DefinePoint[greeterv0.Greeter]("org.example.client.freeze.v1")
	registration := validRegistration(point.ID(), func(grpc.ClientConnInterface) extensions.Provider {
		return point.Provide(frozenGreeter{message: "first"})
	})
	engine := &testEngine{dialer: func(context.Context) (net.Conn, error) {
		return nil, errors.New("unexpected dial")
	}}
	client, err := New(engine, WithGRPCPoint(registration))
	assert.NilError(t, err)
	t.Cleanup(func() { assert.NilError(t, client.Close()) })

	registration.Provider = func(grpc.ClientConnInterface) extensions.Provider {
		return point.Provide(frozenGreeter{message: "second"})
	}
	provider, err := Resolve(client, point)
	assert.NilError(t, err)
	reply, err := provider.Greet(context.Background(), &greeterv0.HelloRequest{Name: "world"})
	assert.NilError(t, err)
	assert.Equal(t, reply.Message, "first")
	assert.Equal(t, engine.dials.Load(), int64(0))
}

type frozenGreeter struct {
	message string
}

func (g frozenGreeter) Greet(context.Context, *greeterv0.HelloRequest) (*greeterv0.HelloReply, error) {
	return &greeterv0.HelloReply{Message: g.message}, nil
}

func TestNewRejectsRegistrationErrors(t *testing.T) {
	t.Parallel()

	engine := &testEngine{dialer: func(context.Context) (net.Conn, error) {
		return nil, errors.New("unexpected dial")
	}}
	provider := func(grpc.ClientConnInterface) extensions.Provider { return extensions.Provider{} }

	t.Run("duplicate identical registrations", func(t *testing.T) {
		registration := validRegistration(greeterv0.Point.ID(), provider)
		_, err := New(engine, WithGRPCPoint(registration), WithGRPCPoint(registration))
		assert.ErrorContains(t, err, "duplicate")
	})
	t.Run("duplicate point registrations", func(t *testing.T) {
		first := validRegistration(greeterv0.Point.ID(), provider)
		second := validRegistration(greeterv0.Point.ID(), func(grpc.ClientConnInterface) extensions.Provider {
			return extensions.Provider{}
		})
		_, err := New(engine, WithGRPCPoint(first), WithGRPCPoint(second))
		assert.ErrorContains(t, err, "duplicate")
	})
	t.Run("invalid point id", func(t *testing.T) {
		registration := validRegistration("Org.example.client.v1", provider)
		_, err := New(engine, WithGRPCPoint(registration))
		assert.ErrorContains(t, err, "invalid")
	})
	t.Run("empty point id", func(t *testing.T) {
		registration := validRegistration("", provider)
		_, err := New(engine, WithGRPCPoint(registration))
		assert.ErrorContains(t, err, "point id is required")
	})
	t.Run("nil provider", func(t *testing.T) {
		_, err := New(engine, WithGRPCPoint(clientpoint.Registration{Point: greeterv0.Point.ID()}))
		assert.ErrorContains(t, err, "nil provider")
	})
}

func TestNewRejectsNilEnginesAndDialers(t *testing.T) {
	t.Parallel()

	_, err := New(nil)
	assert.ErrorContains(t, err, "engine is nil")

	var typedNil *testEngine
	_, err = New(typedNil)
	assert.ErrorContains(t, err, "engine is nil")

	_, err = New(nilDialerEngine{})
	assert.ErrorContains(t, err, "dialer is nil")
}

func TestResolveRejectsUnconfiguredPoint(t *testing.T) {
	t.Parallel()

	_, err := Resolve((*Client)(nil), greeterv0.Point)
	assert.ErrorContains(t, err, "client is nil")

	engine := &testEngine{dialer: func(context.Context) (net.Conn, error) {
		return nil, errors.New("unexpected dial")
	}}
	client, err := New(engine)
	assert.NilError(t, err)
	t.Cleanup(func() { assert.NilError(t, client.Close()) })

	_, err = Resolve(client, greeterv0.Point)
	assert.ErrorContains(t, err, "not configured")
	assert.Equal(t, engine.dials.Load(), int64(0))
}

func TestCloseNilIsSafe(t *testing.T) {
	t.Parallel()

	var client *Client
	assert.NilError(t, client.Close())
}

func TestResolveValidatesAndCachesProvider(t *testing.T) {
	t.Parallel()

	engine := &testEngine{dialer: func(context.Context) (net.Conn, error) {
		return nil, errors.New("unexpected dial")
	}}

	t.Run("wrong provider point", func(t *testing.T) {
		registration := validRegistration(greeterv0.Point.ID(), func(grpc.ClientConnInterface) extensions.Provider {
			return extensions.Provider{Point: echov1.Point.ID(), Impl: testGreeter{}}
		})
		client, err := New(engine, WithGRPCPoint(registration))
		assert.NilError(t, err)
		t.Cleanup(func() { assert.NilError(t, client.Close()) })
		_, err = Resolve(client, greeterv0.Point)
		assert.ErrorContains(t, err, "does not match")
	})

	t.Run("wrong implementation type", func(t *testing.T) {
		registration := validRegistration(greeterv0.Point.ID(), func(grpc.ClientConnInterface) extensions.Provider {
			return extensions.Provider{Point: greeterv0.Point.ID(), Impl: struct{}{}}
		})
		client, err := New(engine, WithGRPCPoint(registration))
		assert.NilError(t, err)
		t.Cleanup(func() { assert.NilError(t, client.Close()) })
		_, err = Resolve(client, greeterv0.Point)
		assert.ErrorContains(t, err, "implementation type")
		assert.ErrorContains(t, err, "want greeterv0.Greeter")
	})

	t.Run("invalid provider is cached after one invocation", func(t *testing.T) {
		var calls atomic.Int64
		registration := validRegistration(greeterv0.Point.ID(), func(grpc.ClientConnInterface) extensions.Provider {
			calls.Add(1)
			return extensions.Provider{Point: echov1.Point.ID(), Impl: testGreeter{}}
		})
		client, err := New(engine, WithGRPCPoint(registration))
		assert.NilError(t, err)
		t.Cleanup(func() { assert.NilError(t, client.Close()) })
		_, firstErr := Resolve(client, greeterv0.Point)
		_, secondErr := Resolve(client, greeterv0.Point)
		assert.ErrorContains(t, firstErr, "does not match")
		assert.ErrorContains(t, secondErr, "does not match")
		assert.Equal(t, calls.Load(), int64(1))
	})
}

func TestResolveConcurrentProviderAndMethodCalls(t *testing.T) {
	t.Parallel()

	listener := newTestServer(t)
	engine := newBufconnEngine(listener)
	var providerCalls atomic.Int64
	registration := greeterpb.ClientPoint
	registration.Provider = func(conn grpc.ClientConnInterface) extensions.Provider {
		providerCalls.Add(1)
		return greeterpb.ClientProvider(conn)
	}
	client, err := New(engine, WithGRPCPoint(registration))
	assert.NilError(t, err)
	t.Cleanup(func() { assert.NilError(t, client.Close()) })

	const callers = 32
	start := make(chan struct{})
	results := make(chan error, callers)
	var group sync.WaitGroup
	group.Add(callers)
	for range callers {
		go func() {
			defer group.Done()
			<-start
			provider, err := Resolve(client, greeterv0.Point)
			if err != nil {
				results <- err
				return
			}
			reply, err := provider.Greet(context.Background(), &greeterv0.HelloRequest{Name: "concurrent"})
			if err != nil {
				results <- err
				return
			}
			if reply == nil || reply.Message != "hello concurrent" {
				results <- fmt.Errorf("unexpected greeting %#v", reply)
			}
		}()
	}
	close(start)
	group.Wait()
	close(results)
	for err := range results {
		assert.NilError(t, err)
	}
	assert.Equal(t, providerCalls.Load(), int64(1))
	assert.Equal(t, engine.dials.Load(), int64(1))
}

func TestCloseIsIdempotentAndClosesPrivateConnection(t *testing.T) {
	t.Parallel()

	listener := newTestServer(t)
	engine := newBufconnEngine(listener)
	client, err := New(engine, WithGRPCPoint(greeterpb.ClientPoint))
	assert.NilError(t, err)
	provider, err := Resolve(client, greeterv0.Point)
	assert.NilError(t, err)
	_, err = provider.Greet(context.Background(), &greeterv0.HelloRequest{Name: "close"})
	assert.NilError(t, err)
	conn := <-engine.conns

	assert.Equal(t, conn.closes.Load(), int64(0))
	assert.NilError(t, client.Close())
	closedCalls := conn.closes.Load()
	assert.Assert(t, closedCalls > 0, "Close must close the private connection")
	assert.NilError(t, client.Close())
	assert.Equal(t, conn.closes.Load(), closedCalls, "a repeated Close must not close again")
}

func TestResolveAfterCloseFailsWithoutInvokingProvider(t *testing.T) {
	t.Parallel()

	var calls atomic.Int64
	registration := validRegistration(greeterv0.Point.ID(), func(grpc.ClientConnInterface) extensions.Provider {
		calls.Add(1)
		return greeterpb.ClientProvider(nil)
	})
	engine := &testEngine{dialer: func(context.Context) (net.Conn, error) {
		return nil, errors.New("unexpected dial")
	}}
	client, err := New(engine, WithGRPCPoint(registration))
	assert.NilError(t, err)
	assert.NilError(t, client.Close())

	_, err = Resolve(client, greeterv0.Point)
	assert.ErrorContains(t, err, "client is closed")
	assert.Equal(t, calls.Load(), int64(0))
}

func TestConcurrentResolveAndCloseAreOrdered(t *testing.T) {
	t.Parallel()

	engine := &testEngine{dialer: func(context.Context) (net.Conn, error) {
		return nil, errors.New("unexpected dial")
	}}
	entered := make(chan struct{})
	release := make(chan struct{})
	var providerCalls atomic.Int64
	registration := validRegistration(greeterv0.Point.ID(), func(conn grpc.ClientConnInterface) extensions.Provider {
		providerCalls.Add(1)
		close(entered)
		<-release
		return greeterpb.ClientProvider(conn)
	})
	client, err := New(engine, WithGRPCPoint(registration))
	assert.NilError(t, err)

	firstResolve := make(chan error, 1)
	go func() {
		_, resolveErr := Resolve(client, greeterv0.Point)
		firstResolve <- resolveErr
	}()
	<-entered

	const concurrentCallers = 16
	results := make(chan error, concurrentCallers)
	var group sync.WaitGroup
	group.Add(concurrentCallers)
	for i := range concurrentCallers {
		go func(i int) {
			defer group.Done()
			if i%2 == 0 {
				_, resolveErr := Resolve(client, greeterv0.Point)
				if resolveErr != nil && resolveErr.Error() != "client: client is closed" {
					results <- resolveErr
				}
				return
			}
			if closeErr := client.Close(); closeErr != nil {
				results <- closeErr
			}
		}(i)
	}
	close(release)
	assert.NilError(t, <-firstResolve)
	group.Wait()
	close(results)
	for err := range results {
		assert.NilError(t, err)
	}
	assert.Equal(t, providerCalls.Load(), int64(1))
	assert.NilError(t, client.Close())
}
