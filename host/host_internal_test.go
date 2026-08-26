package host

import (
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/moby/extensions"
	"github.com/moby/extensions/clientpoint"
	servicev0 "github.com/moby/extensions/extpoints/service/v0"
	"github.com/moby/extensions/internal/broker"
	"github.com/moby/extensions/internal/launcher"
	echopb "github.com/moby/extensions/internal/launcher/echo/v1/protogen"
	"github.com/moby/extensions/serverpoint"
	"google.golang.org/grpc"
	"gotest.tools/v3/assert"
)

const lifecycleExtensionID = extensions.ExtensionID("org.example.lifecycle.v1")

var _ PointPolicy = PointPolicyFunc(nil)

func shortTempDir(t *testing.T) string {
	t.Helper()
	// Keep socket paths relative so they fit Windows' AF_UNIX path limit.
	dir, err := os.MkdirTemp(".", "m")
	assert.NilError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func buildLifecycleExtension(t *testing.T) (dir, bin string) {
	t.Helper()
	dir = t.TempDir()
	name := string(lifecycleExtensionID)
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	bin = filepath.Join(dir, name)
	build := exec.Command("go", "build", "-o", bin,
		"github.com/moby/extensions/host/testdata/lifecycle")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build lifecycle extension: %v\n%s", err, out)
	}
	return dir, bin
}

func processProbeConfig(probeFile string, failInit bool) map[extensions.ExtensionID]extensions.Config {
	return map[extensions.ExtensionID]extensions.Config{
		lifecycleExtensionID: {
			"probeFile": probeFile,
			"failInit":  failInit,
		},
	}
}

func executableIdentity(id extensions.ExtensionID) extensions.ExtensionIdentity {
	return executableIdentityAtPath(id, "test-executable")
}

func executableIdentityAtPath(id extensions.ExtensionID, path string) extensions.ExtensionIdentity {
	return extensions.ExtensionIdentity{
		ID: id,
		Origin: extensions.ExtensionOrigin{
			Kind:       extensions.ExtensionOriginExecutable,
			Executable: &extensions.ExecutableOrigin{Path: path},
		},
	}
}

func processProbeAddress(t *testing.T, probeFile string) string {
	t.Helper()
	address, err := os.ReadFile(probeFile)
	assert.NilError(t, err)
	return string(address)
}

func assertProcessRunning(t *testing.T, probeFile string) {
	t.Helper()
	listener, err := net.Listen("tcp", processProbeAddress(t, probeFile))
	if err == nil {
		_ = listener.Close()
		t.Fatal("process probe was available while the extension should be running")
	}
}

func assertProcessReleased(t *testing.T, probeFile string) {
	t.Helper()
	listener, err := net.Listen("tcp", processProbeAddress(t, probeFile))
	assert.NilError(t, err, "process probe was not released")
	assert.NilError(t, listener.Close())
}

func TestExtensionFromHostedRejectsUnsupportedPoints(t *testing.T) {
	const supported = extensions.PointID("org.mobyproject.extension.supported.v1")
	const offered = extensions.PointID("org.example.own.api.v1")
	const unsupported = extensions.PointID("org.example.unknown.v1")

	providers := map[extensions.PointID]clientpoint.Provider{
		supported: func(grpc.ClientConnInterface) extensions.Provider {
			return extensions.Provider{Point: supported, Impl: "impl"}
		},
	}

	ext, err := extensionFromHosted(hostedExtension{
		identity: executableIdentity("org.example.ext.v1"),
		points:   []extensions.PointID{supported},
	}, providers)
	assert.NilError(t, err)
	assert.Equal(t, len(ext.Declaration().Providers), 1)

	_, err = extensionFromHosted(hostedExtension{
		identity: executableIdentity("org.example.ext.v1"),
		points:   []extensions.PointID{supported, unsupported},
	}, providers)
	assert.ErrorContains(t, err, "unsupported point")
	assert.ErrorContains(t, err, string(unsupported))

	ext, err = extensionFromHosted(hostedExtension{
		identity: executableIdentity("org.example.ext.v1"),
		points:   []extensions.PointID{supported, offered, servicev0.Point.ID()},
		offered:  []extensions.PointID{offered},
	}, providers)
	assert.NilError(t, err)
	assert.Equal(t, len(ext.Declaration().Providers), 1)
}

func TestExtensionFromHostedForwardsBrokerConfig(t *testing.T) {
	const id = extensions.ExtensionID("org.example.hosted.v1")
	want := extensions.Config{"message": "configured"}
	var got extensions.Config
	ext, err := extensionFromHosted(hostedExtension{
		identity: executableIdentity(id),
		initialize: func(_ context.Context, config extensions.Config) error {
			got = config
			return nil
		},
	}, nil)
	assert.NilError(t, err)

	b := broker.New()
	assert.NilError(t, registerExecutableForTest(b, ext))
	assert.NilError(t, b.Init(context.Background(), map[extensions.ExtensionID]extensions.Config{id: want}))
	assert.DeepEqual(t, got, want)
}

func TestExtensionFromHostedRunsSemanticShutdown(t *testing.T) {
	shutdown := false
	ext, err := extensionFromHosted(hostedExtension{
		identity:   executableIdentity("org.example.hosted.v1"),
		initialize: func(context.Context, extensions.Config) error { return nil },
		shutdown: func(context.Context) error {
			shutdown = true
			return nil
		},
	}, nil)
	assert.NilError(t, err)

	b := broker.New()
	assert.NilError(t, registerExecutableForTest(b, ext))
	assert.NilError(t, b.Init(context.Background(), nil))
	assert.NilError(t, b.Shutdown(context.Background()))
	assert.Assert(t, shutdown, "the broker did not run hosted semantic shutdown")
}

func TestClientProviderMap(t *testing.T) {
	const pointA = extensions.PointID("org.example.a.v1")
	const pointB = extensions.PointID("org.example.b.v1")
	build := func(grpc.ClientConnInterface) extensions.Provider {
		return extensions.Provider{}
	}

	m, err := clientProviderMap([]clientpoint.Registration{
		{Point: pointA, Provider: build},
		{Point: pointB, Provider: build},
	})
	assert.NilError(t, err)
	assert.Equal(t, len(m), 2)
	_, okA := m[pointA]
	_, okB := m[pointB]
	assert.Assert(t, okA)
	assert.Assert(t, okB)

	_, err = clientProviderMap([]clientpoint.Registration{
		{Point: pointA, Provider: build},
		{Point: pointA, Provider: build},
	})
	assert.ErrorContains(t, err, "duplicate client provider")
	assert.ErrorContains(t, err, string(pointA))
}

func newProviderExtension(id extensions.ExtensionID, point extensions.PointID) extensions.Extension {
	return extensions.New(extensions.Declaration{
		ID:        id,
		Providers: []extensions.Provider{{Point: point, Impl: "impl"}},
	})
}

func registerExecutableForTest(b *broker.Broker, ext extensions.Extension) error {
	return b.Register(extensions.ExtensionIdentity{
		ID: ext.Declaration().ID,
		Origin: extensions.ExtensionOrigin{
			Kind:       extensions.ExtensionOriginExecutable,
			Executable: &extensions.ExecutableOrigin{Path: "test-executable"},
		},
	}, ext)
}

func TestServeCallback(t *testing.T) {
	const dep = extensions.PointID("org.mobyproject.extension.dep.v1")

	newDep := func(served *[]any) serverpoint.Registration {
		return serverpoint.Registration{
			Point: dep,
			Register: func(_ grpc.ServiceRegistrar, impl any) {
				*served = append(*served, impl)
			},
		}
	}

	t.Run("zero providers is skipped", func(t *testing.T) {
		b := broker.New()
		var served []any
		endpoint := filepath.Join(shortTempDir(t), "callback.sock")
		srv, err := serveCallback(endpoint, []serverpoint.Registration{newDep(&served)}, b)
		assert.NilError(t, err)
		if srv != nil {
			defer srv.Stop()
		}
		assert.Equal(t, len(served), 0)
	})

	t.Run("one provider is registered", func(t *testing.T) {
		b := broker.New()
		assert.NilError(t, registerExecutableForTest(b, newProviderExtension("org.example.a.v1", dep)))
		var served []any
		endpoint := filepath.Join(shortTempDir(t), "callback.sock")
		srv, err := serveCallback(endpoint, []serverpoint.Registration{newDep(&served)}, b)
		assert.NilError(t, err)
		assert.Assert(t, srv != nil)
		defer srv.Stop()
		assert.Equal(t, len(served), 1)
	})

	t.Run("executable provider replaces builtin", func(t *testing.T) {
		b := broker.New()
		builtin := newProviderExtension("org.example.builtin.v1", dep)
		assert.NilError(t, b.Register(extensions.ExtensionIdentity{
			ID:     builtin.Declaration().ID,
			Origin: extensions.ExtensionOrigin{Kind: extensions.ExtensionOriginBuiltin},
		}, builtin))
		executable := extensions.New(extensions.Declaration{
			ID:        "org.example.executable.v1",
			Providers: []extensions.Provider{{Point: dep, Impl: "executable"}},
		})
		assert.NilError(t, registerExecutableForTest(b, executable))
		var served []any
		endpoint := filepath.Join(shortTempDir(t), "callback.sock")
		srv, err := serveCallback(endpoint, []serverpoint.Registration{newDep(&served)}, b)
		assert.NilError(t, err)
		assert.Assert(t, srv != nil)
		defer srv.Stop()
		assert.DeepEqual(t, served, []any{"executable"})
	})

	t.Run("multiple providers is an error", func(t *testing.T) {
		b := broker.New()
		assert.NilError(t, registerExecutableForTest(b, newProviderExtension("org.example.a.v1", dep)))
		assert.NilError(t, registerExecutableForTest(b, newProviderExtension("org.example.b.v1", dep)))
		var served []any
		endpoint := filepath.Join(shortTempDir(t), "callback.sock")
		srv, err := serveCallback(endpoint, []serverpoint.Registration{newDep(&served)}, b)
		if srv != nil {
			srv.Stop()
		}
		assert.ErrorContains(t, err, string(dep))
		assert.Equal(t, len(served), 0)
	})
}

func TestSinglePointRejectsTwoProviders(t *testing.T) {
	const point = extensions.PointID("org.example.decider.v1")
	ext := func(id extensions.ExtensionID) extensions.Extension {
		return extensions.New(extensions.Declaration{
			ID:        id,
			Providers: []extensions.Provider{{Point: point, Impl: struct{}{}}},
		})
	}
	singleReg := clientpoint.Registration{
		Point:    point,
		Provider: func(grpc.ClientConnInterface) extensions.Provider { return extensions.Provider{} },
		Single:   true,
	}

	_, err := New(context.Background(),
		WithRuntimeDir(t.TempDir()),
		WithExtensions(ext("org.example.one.v1"), ext("org.example.two.v1")),
		WithClientProviders(singleReg),
	)
	assert.ErrorContains(t, err, `point "org.example.decider.v1" admits a single provider`)
	assert.ErrorContains(t, err, "org.example.one.v1")
	assert.ErrorContains(t, err, "org.example.two.v1")

	h, err := New(context.Background(),
		WithRuntimeDir(t.TempDir()),
		WithExtensions(ext("org.example.one.v1")),
		WithClientProviders(singleReg),
	)
	assert.NilError(t, err)
	assert.NilError(t, h.Shutdown(context.Background()))
}

func TestProviderAdmissionPolicy(t *testing.T) {
	const point = extensions.PointID("org.example.internal.v1")
	const id = extensions.ExtensionID("org.example.provider.v1")
	ext := newProviderExtension(id, point)
	wantIdentity := extensions.ExtensionIdentity{ID: id, Origin: extensions.ExtensionOrigin{Kind: extensions.ExtensionOriginBuiltin}}
	var gotIdentity extensions.ExtensionIdentity
	var gotPoint extensions.PointID

	_, err := New(context.Background(),
		WithRuntimeDir(t.TempDir()),
		WithExtensions(ext),
		WithProviderPolicy(PointPolicyFunc(func(identity extensions.ExtensionIdentity, policyPoint extensions.PointID) bool {
			gotIdentity = identity
			gotPoint = policyPoint
			return false
		})),
	)
	assert.ErrorContains(t, err, `extension "org.example.provider.v1"`)
	assert.ErrorContains(t, err, `origin "builtin"`)
	assert.ErrorContains(t, err, `point "org.example.internal.v1"`)
	assert.Equal(t, gotIdentity, wantIdentity)
	assert.Equal(t, gotPoint, point)

	h, err := New(context.Background(),
		WithRuntimeDir(t.TempDir()),
		WithExtensions(ext),
	)
	assert.NilError(t, err)
	t.Cleanup(func() { assert.NilError(t, h.Shutdown(context.Background())) })
	providers := h.Providers(point)
	assert.Equal(t, len(providers), 1)
	assert.Equal(t, providers[0].Identity, wantIdentity)
}

// TestProviderAndPublicationPolicyPoints verifies that provider admission and
// publication use their respective Point IDs.
func TestProviderAndPublicationPolicyPoints(t *testing.T) {
	const id = extensions.ExtensionID("org.example.offered.v1")
	const realPoint = extensions.PointID("org.example.internal.v1")
	pointDef := extensions.DefinePoint[any](realPoint)
	ext := extensions.New(extensions.Declaration{
		ID: id,
		Providers: []extensions.Provider{
			pointDef.Provide(struct{}{}),
			servicev0.Offer(pointDef),
		},
	})
	server := serverpoint.Registration{
		Point: realPoint,
		Register: func(registrar grpc.ServiceRegistrar, impl any) {
			registrar.RegisterService(&grpc.ServiceDesc{
				ServiceName: "example.API",
				HandlerType: (*any)(nil),
			}, impl)
		},
	}
	var policyPoints []extensions.PointID
	h, err := New(context.Background(),
		WithRuntimeDir(t.TempDir()),
		WithExtensions(ext),
		WithPointServers(server),
		WithProviderPolicy(PointPolicyFunc(func(_ extensions.ExtensionIdentity, point extensions.PointID) bool {
			policyPoints = append(policyPoints, point)
			return point == realPoint
		})),
	)
	assert.NilError(t, err)
	t.Cleanup(func() { assert.NilError(t, h.Shutdown(context.Background())) })
	assert.DeepEqual(t, policyPoints, []extensions.PointID{realPoint, servicev0.Point.ID()})
	provider, err := h.Provider(realPoint, id)
	assert.NilError(t, err)
	assert.Equal(t, provider, struct{}{})
	assert.DeepEqual(t, h.PublishedServicesForPoint(realPoint), map[extensions.ExtensionID][]string{})
}

// TestLaunchedExtensionCarriesShutdown verifies launched extensions participate
// in broker shutdown ordering.
func TestLaunchedExtensionCarriesShutdown(t *testing.T) {
	const point = extensions.PointID("org.mobyproject.extension.supported.v1")
	providers := map[extensions.PointID]clientpoint.Provider{
		point: func(grpc.ClientConnInterface) extensions.Provider {
			return extensions.Provider{Point: point, Impl: "impl"}
		},
	}

	hosted := hostedExtensionFromLaunched(&launcher.Launched{
		ID:     "org.example.ext.v1",
		Path:   "test-executable",
		Points: []launcher.LaunchedPoint{{ID: point}},
	})
	ext, err := extensionFromHosted(hosted, providers)
	assert.NilError(t, err)
	assert.Assert(t, ext.Declaration().Shutdown != nil,
		"a launched extension must declare a Shutdown so the broker stops it in dependency order")
}

func TestProcessResourceCleanup(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and launches a helper binary")
	}
	dir, bin := buildLifecycleExtension(t)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	t.Run("provider policy denial", func(t *testing.T) {
		probeFile := filepath.Join(t.TempDir(), "probe")
		var gotIdentity extensions.ExtensionIdentity
		_, err := New(ctx,
			WithRuntimeDir(shortTempDir(t)),
			WithDirs(dir),
			WithClientProviders(echopb.ClientPoint),
			WithExtensionConfig(processProbeConfig(probeFile, false)),
			WithProviderPolicy(PointPolicyFunc(func(identity extensions.ExtensionIdentity, point extensions.PointID) bool {
				gotIdentity = identity
				return false
			})),
		)
		assert.ErrorContains(t, err, `extension "org.example.lifecycle.v1"`)
		assert.ErrorContains(t, err, `origin "executable"`)
		assert.ErrorContains(t, err, `point "moby.extensions.internal.launcher.echo.v1"`)
		assert.DeepEqual(t, gotIdentity, executableIdentityAtPath(lifecycleExtensionID, bin))
		assertProcessReleased(t, probeFile)
	})

	t.Run("adaptation error", func(t *testing.T) {
		probeFile := filepath.Join(t.TempDir(), "probe")
		_, _, err := loadProcess(ctx, launcher.Launcher{
			RuntimeDir:      shortTempDir(t),
			ExtensionConfig: processProbeConfig(probeFile, false),
		}, bin, nil)
		assert.ErrorContains(t, err, "unsupported point")
		assertProcessReleased(t, probeFile)
	})

	t.Run("register error", func(t *testing.T) {
		probeFile := filepath.Join(t.TempDir(), "probe")
		_, err := New(ctx,
			WithRuntimeDir(shortTempDir(t)),
			WithExtensions(extensions.New(extensions.Declaration{
				ID: lifecycleExtensionID,
			})),
			WithDirs(dir),
			WithClientProviders(echopb.ClientPoint),
			WithExtensionConfig(processProbeConfig(probeFile, false)),
		)
		assert.ErrorContains(t, err, "already registered")
		assertProcessReleased(t, probeFile)
	})

	t.Run("partial init error", func(t *testing.T) {
		probeFile := filepath.Join(t.TempDir(), "probe")
		semanticCloseErr := errors.New("semantic cleanup failure")
		semanticShutdownCalled := false
		processRunningDuringSemanticShutdown := false
		var probeObservationErr error
		initializedBuiltin := extensions.New(extensions.Declaration{
			ID: "org.example.initialized.v1",
			Init: func(context.Context, extensions.Config, extensions.Resolver) error {
				return nil
			},
			Shutdown: func(context.Context) error {
				semanticShutdownCalled = true
				// Construction cleanup must run broker shutdown before releasing the
				// process resource.
				address, err := os.ReadFile(probeFile)
				if err != nil {
					probeObservationErr = err
					return semanticCloseErr
				}
				listener, err := net.Listen("tcp", string(address))
				processRunningDuringSemanticShutdown = err != nil
				if listener != nil {
					_ = listener.Close()
				}
				return semanticCloseErr
			},
		})

		_, err := New(ctx,
			WithRuntimeDir(shortTempDir(t)),
			WithExtensions(initializedBuiltin),
			WithDirs(dir),
			WithClientProviders(echopb.ClientPoint),
			WithExtensionConfig(processProbeConfig(probeFile, true)),
		)
		assert.ErrorContains(t, err, "requested initialization failure")
		assert.Assert(t, semanticShutdownCalled)
		assert.NilError(t, probeObservationErr)
		assert.Assert(t, processRunningDuringSemanticShutdown,
			"process resource was released before semantic broker shutdown")
		assert.Assert(t, !errors.Is(err, semanticCloseErr),
			"construction cleanup error replaced or was joined with the init error")
		assertProcessReleased(t, probeFile)
	})

	t.Run("normal shutdown", func(t *testing.T) {
		probeFile := filepath.Join(t.TempDir(), "probe")
		h, err := New(ctx,
			WithRuntimeDir(shortTempDir(t)),
			WithExtensions(extensions.New(extensions.Declaration{
				ID: "org.example.builtin.v1",
			})),
			WithDirs(dir),
			WithClientProviders(echopb.ClientPoint),
			WithExtensionConfig(processProbeConfig(probeFile, false)),
		)
		assert.NilError(t, err)
		shutdown := false
		t.Cleanup(func() {
			if !shutdown {
				_ = h.Shutdown(context.Background())
			}
		})
		assert.Equal(t, len(h.loaded), 1,
			"only the process-backed extension should own a loaded resource")
		assertProcessRunning(t, probeFile)

		err = h.Shutdown(context.Background())
		shutdown = true
		assert.NilError(t, err)
		assertProcessReleased(t, probeFile)
	})
}

func TestCloseLoadedErrClosesInReverseOrderAndJoinsErrors(t *testing.T) {
	firstErr := errors.New("first close")
	secondErr := errors.New("second close")
	var closed []string
	loaded := []loadedExtension{
		{close: func(context.Context) error {
			closed = append(closed, "first")
			return firstErr
		}},
		{close: func(context.Context) error {
			closed = append(closed, "second")
			return secondErr
		}},
	}

	err := closeLoadedErr(context.Background(), loaded)
	assert.DeepEqual(t, closed, []string{"second", "first"})
	assert.Assert(t, errors.Is(err, firstErr))
	assert.Assert(t, errors.Is(err, secondErr))
}

func TestCloseLoadedSuppressesConstructionCleanupErrors(t *testing.T) {
	closeErr := errors.New("close failure")
	var closed []string
	closeLoaded(context.Background(), []loadedExtension{
		{close: func(context.Context) error {
			closed = append(closed, "first")
			return closeErr
		}},
		{close: func(context.Context) error {
			closed = append(closed, "second")
			return closeErr
		}},
	})
	assert.DeepEqual(t, closed, []string{"second", "first"})
}

func TestHostShutdownJoinsSemanticAndResourceErrors(t *testing.T) {
	semanticErr := errors.New("semantic shutdown")
	resourceErr := errors.New("resource close")
	var order []string
	b := broker.New()
	assert.NilError(t, registerExecutableForTest(b, extensions.New(extensions.Declaration{
		ID: "org.example.shutdown.v1",
		Shutdown: func(context.Context) error {
			order = append(order, "semantic")
			return semanticErr
		},
	})))
	assert.NilError(t, b.Init(context.Background(), nil))
	h := &Host{
		broker: b,
		loaded: []loadedExtension{{close: func(context.Context) error {
			order = append(order, "resource")
			return resourceErr
		}}},
	}

	err := h.Shutdown(context.Background())
	assert.DeepEqual(t, order, []string{"semantic", "resource"})
	assert.Assert(t, errors.Is(err, semanticErr))
	assert.Assert(t, errors.Is(err, resourceErr))
}

func TestApproveProcessPublications(t *testing.T) {
	const point = extensions.PointID("org.example.api.v1")
	const otherPoint = extensions.PointID("org.example.other.v1")
	launched := &launcher.Launched{
		ID:            "org.example.first.v1",
		OfferedPoints: []extensions.PointID{point, otherPoint},
		ProviderServices: map[extensions.PointID][]string{
			point:      {"example.API"},
			otherPoint: {"example.Other"},
		},
	}
	identity := executableIdentity(launched.ID)
	allow := PointPolicyFunc(func(extensions.ExtensionIdentity, extensions.PointID) bool { return true })
	deny := PointPolicyFunc(func(extensions.ExtensionIdentity, extensions.PointID) bool { return false })

	t.Run("nil policy denies", func(t *testing.T) {
		published := make(map[extensions.ExtensionID]map[extensions.PointID][]string)
		assert.NilError(t, approveProcessPublications(identity, launched, nil, published, map[string]extensions.ExtensionID{}, nil))
		assert.Equal(t, len(published), 0)
	})

	t.Run("nil function policy denies", func(t *testing.T) {
		published := make(map[extensions.ExtensionID]map[extensions.PointID][]string)
		assert.NilError(t, approveProcessPublications(identity, launched, PointPolicyFunc(nil), published, map[string]extensions.ExtensionID{}, nil))
		assert.Equal(t, len(published), 0)
	})

	t.Run("denied offer is omitted", func(t *testing.T) {
		published := make(map[extensions.ExtensionID]map[extensions.PointID][]string)
		assert.NilError(t, approveProcessPublications(identity, launched, deny, published, map[string]extensions.ExtensionID{}, nil))
		assert.Equal(t, len(published), 0)
	})

	t.Run("allowed offers are copied", func(t *testing.T) {
		published := make(map[extensions.ExtensionID]map[extensions.PointID][]string)
		assert.NilError(t, approveProcessPublications(identity, launched, allow, published, map[string]extensions.ExtensionID{}, nil))
		assert.DeepEqual(t, published[launched.ID][point], []string{"example.API"})
		assert.DeepEqual(t, published[launched.ID][otherPoint], []string{"example.Other"})
		launched.ProviderServices[point][0] = "changed"
		assert.DeepEqual(t, published[launched.ID][point], []string{"example.API"})
		launched.ProviderServices[point][0] = "example.API"
	})

	t.Run("provider policy is called once with service metadata", func(t *testing.T) {
		var policyPoints []extensions.PointID
		policy := PointPolicyFunc(func(_ extensions.ExtensionIdentity, point extensions.PointID) bool {
			policyPoints = append(policyPoints, point)
			return point == servicev0.Point.ID()
		})
		published := make(map[extensions.ExtensionID]map[extensions.PointID][]string)
		assert.NilError(t, approveProcessPublications(identity, launched, policy, published, map[string]extensions.ExtensionID{}, nil))
		assert.DeepEqual(t, policyPoints, []extensions.PointID{servicev0.Point.ID()})
		assert.Equal(t, len(published[launched.ID]), 2)
	})

	t.Run("missing service is rejected", func(t *testing.T) {
		missing := &launcher.Launched{ID: launched.ID, OfferedPoints: []extensions.PointID{point}}
		err := approveProcessPublications(identity, missing, allow, make(map[extensions.ExtensionID]map[extensions.PointID][]string), map[string]extensions.ExtensionID{}, nil)
		assert.ErrorContains(t, err, "without a gRPC service")
	})

	t.Run("reserved service is rejected", func(t *testing.T) {
		err := approveProcessPublications(identity, launched, allow, make(map[extensions.ExtensionID]map[extensions.PointID][]string), map[string]extensions.ExtensionID{}, map[string]bool{"example.API": true})
		assert.ErrorContains(t, err, `cannot publish reserved gRPC service "example.API"`)
	})

	t.Run("service collision is rejected", func(t *testing.T) {
		err := approveProcessPublications(identity, launched, allow, make(map[extensions.ExtensionID]map[extensions.PointID][]string), map[string]extensions.ExtensionID{"example.API": "org.example.other.v1"}, nil)
		assert.ErrorContains(t, err, `extensions "org.example.other.v1" and "org.example.first.v1" both publish gRPC service "example.API"`)
	})
}

func TestInProcessPublicationValidation(t *testing.T) {
	pointDefinition := extensions.DefinePoint[any]("org.example.api.v1")
	point := pointDefinition.ID()
	ext := extensions.New(extensions.Declaration{
		ID: "org.example.extension.v1",
		Providers: []extensions.Provider{
			pointDefinition.Provide(struct{}{}),
			servicev0.Offer(pointDefinition),
		},
	})
	identity := extensions.ExtensionIdentity{ID: ext.Declaration().ID, Origin: extensions.ExtensionOrigin{Kind: extensions.ExtensionOriginBuiltin}}
	allow := PointPolicyFunc(func(extensions.ExtensionIdentity, extensions.PointID) bool { return true })
	deny := PointPolicyFunc(func(extensions.ExtensionIdentity, extensions.PointID) bool { return false })

	t.Run("denied offer needs no adapter", func(t *testing.T) {
		services, err := collectInProcessPublications(identity, ext, deny, nil, make(map[extensions.ExtensionID]map[extensions.PointID][]string), map[string]extensions.ExtensionID{}, nil)
		assert.NilError(t, err)
		assert.Equal(t, len(services), 0)
	})

	t.Run("allowed offer needs adapter", func(t *testing.T) {
		_, err := collectInProcessPublications(identity, ext, allow, nil, make(map[extensions.ExtensionID]map[extensions.PointID][]string), map[string]extensions.ExtensionID{}, nil)
		assert.ErrorContains(t, err, "has no server registration")
	})

	registration := serverpoint.Registration{
		Point: point,
		Register: func(registrar grpc.ServiceRegistrar, impl any) {
			registrar.RegisterService(&grpc.ServiceDesc{ServiceName: "example.API", HandlerType: (*any)(nil)}, impl)
		},
	}
	servers := map[extensions.PointID]serverpoint.Registration{point: registration}

	t.Run("reserved service is rejected", func(t *testing.T) {
		_, err := collectInProcessPublications(identity, ext, allow, servers, make(map[extensions.ExtensionID]map[extensions.PointID][]string), map[string]extensions.ExtensionID{}, map[string]bool{"example.API": true})
		assert.ErrorContains(t, err, `cannot publish reserved gRPC service "example.API"`)
	})

	t.Run("process service collision is rejected", func(t *testing.T) {
		_, err := collectInProcessPublications(identity, ext, allow, servers, make(map[extensions.ExtensionID]map[extensions.PointID][]string), map[string]extensions.ExtensionID{"example.API": "org.example.process.v1"}, nil)
		assert.ErrorContains(t, err, `extensions "org.example.process.v1" and "org.example.extension.v1" both publish gRPC service "example.API"`)
	})

	t.Run("in-process service collision is rejected", func(t *testing.T) {
		published := make(map[extensions.ExtensionID]map[extensions.PointID][]string)
		owners := make(map[string]extensions.ExtensionID)
		_, err := collectInProcessPublications(identity, ext, allow, servers, published, owners, nil)
		assert.NilError(t, err)
		other := extensions.New(extensions.Declaration{
			ID: "org.example.other.v1",
			Providers: []extensions.Provider{
				pointDefinition.Provide(struct{}{}),
				servicev0.Offer(pointDefinition),
			},
		})
		otherIdentity := extensions.ExtensionIdentity{ID: other.Declaration().ID, Origin: extensions.ExtensionOrigin{Kind: extensions.ExtensionOriginBuiltin}}
		_, err = collectInProcessPublications(otherIdentity, other, allow, servers, published, owners, nil)
		assert.ErrorContains(t, err, `extensions "org.example.extension.v1" and "org.example.other.v1" both publish gRPC service "example.API"`)
	})
}

func TestPublishedServicesForPointUsesPolicyFilteredIndex(t *testing.T) {
	const point = extensions.PointID("org.example.publication.v1")
	h := &Host{publishedServices: map[extensions.ExtensionID]map[extensions.PointID][]string{
		"org.example.first.v1": {
			point: {"example.First", "example.Second"},
		},
		"org.example.empty.v1": {
			point: nil,
		},
	}}

	got := h.PublishedServicesForPoint(point)
	assert.DeepEqual(t, got, map[extensions.ExtensionID][]string{
		"org.example.first.v1": {"example.First", "example.Second"},
	})
	got["org.example.first.v1"][0] = "changed"
	assert.Equal(t, h.publishedServices["org.example.first.v1"][point][0], "example.First")
	assert.DeepEqual(t, h.PublishedServicesForPoint("org.example.unknown.v1"),
		map[extensions.ExtensionID][]string{})
}
