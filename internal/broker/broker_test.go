// SPDX-FileCopyrightText: Copyright The Moby Authors
// SPDX-License-Identifier: Apache-2.0

package broker

import (
	"context"
	"errors"
	"slices"
	"sync"
	"testing"

	"github.com/moby/extensions"
	"gotest.tools/v3/assert"
)

type pingProvider struct{}

func (pingProvider) Ping(context.Context) error { return nil }

func registerExecutable(b *Broker, ext extensions.Extension) error {
	return b.Register(extensions.ExtensionIdentity{
		ID: ext.Declaration().ID,
		Origin: extensions.ExtensionOrigin{
			Kind:       extensions.ExtensionOriginExecutable,
			Executable: &extensions.ExecutableOrigin{Path: "test-executable"},
		},
	}, ext)
}

func TestInitOrdersDependencies(t *testing.T) {
	ctx := t.Context()
	b := New()
	var order []extensions.ExtensionID

	err := registerExecutable(b, extensions.New(extensions.Declaration{
		ID:        "org.test.dependent.v1",
		Providers: []extensions.Provider{{Point: "dependent.point", Impl: pingProvider{}}},
		Dependencies: []extensions.Dependency{
			{Point: "dependency.point"},
			{Extension: "org.test.named-dependency.v1"},
		},
		Init: func(context.Context, extensions.Config, extensions.Resolver) error {
			order = append(order, "org.test.dependent.v1")
			return nil
		},
	}))
	assert.NilError(t, err)
	err = registerExecutable(b, extensions.New(extensions.Declaration{
		ID:        "org.test.point-dependency.v1",
		Providers: []extensions.Provider{{Point: "dependency.point", Impl: pingProvider{}}},
		Init: func(context.Context, extensions.Config, extensions.Resolver) error {
			order = append(order, "org.test.point-dependency.v1")
			return nil
		},
	}))
	assert.NilError(t, err)
	err = registerExecutable(b, extensions.New(extensions.Declaration{
		ID: "org.test.named-dependency.v1",
		Init: func(context.Context, extensions.Config, extensions.Resolver) error {
			order = append(order, "org.test.named-dependency.v1")
			return nil
		},
	}))
	assert.NilError(t, err)

	assert.NilError(t, b.Init(ctx, nil))

	dependentIndex := slices.Index(order, extensions.ExtensionID("org.test.dependent.v1"))
	pointDependencyIndex := slices.Index(order, extensions.ExtensionID("org.test.point-dependency.v1"))
	namedDependencyIndex := slices.Index(order, extensions.ExtensionID("org.test.named-dependency.v1"))
	assert.Check(t, dependentIndex >= 0)
	assert.Check(t, pointDependencyIndex >= 0)
	assert.Check(t, namedDependencyIndex >= 0)
	assert.Check(t, pointDependencyIndex < dependentIndex)
	assert.Check(t, namedDependencyIndex < dependentIndex)
}

func TestShutdownOrdersDependenciesInReverse(t *testing.T) {
	b := New()
	var order []extensions.ExtensionID
	assert.NilError(t, registerExecutable(b, extensions.New(extensions.Declaration{
		ID:           "org.test.dependent.v1",
		Dependencies: []extensions.Dependency{{Extension: "org.test.dependency.v1"}},
		Shutdown: func(context.Context) error {
			order = append(order, "org.test.dependent.v1")
			return nil
		},
	})))
	assert.NilError(t, registerExecutable(b, extensions.New(extensions.Declaration{
		ID: "org.test.dependency.v1",
		Shutdown: func(context.Context) error {
			order = append(order, "org.test.dependency.v1")
			return nil
		},
	})))
	assert.NilError(t, b.Init(t.Context(), nil))

	err := b.Shutdown(t.Context())
	assert.NilError(t, err)
	assert.DeepEqual(t, order, []extensions.ExtensionID{"org.test.dependent.v1", "org.test.dependency.v1"})
}

func TestShutdownSkipsUninitialized(t *testing.T) {
	b := New()
	var shutdown []extensions.ExtensionID
	assert.NilError(t, registerExecutable(b, extensions.New(extensions.Declaration{
		ID: "org.test.registered-not-initialized.v1",
		Shutdown: func(context.Context) error {
			shutdown = append(shutdown, "org.test.registered-not-initialized.v1")
			return nil
		},
	})))

	assert.NilError(t, b.Shutdown(t.Context()))
	assert.Check(t, len(shutdown) == 0, "Shutdown ran on an uninitialized extension: %v", shutdown)
}

func TestShutdownUnwindsPartialInit(t *testing.T) {
	b := New()
	var order []extensions.ExtensionID
	shutdownRecorder := func(id extensions.ExtensionID) func(context.Context) error {
		return func(context.Context) error {
			order = append(order, id)
			return nil
		}
	}
	assert.NilError(t, registerExecutable(b, extensions.New(extensions.Declaration{
		ID:       "org.test.first.v1",
		Init:     func(context.Context, extensions.Config, extensions.Resolver) error { return nil },
		Shutdown: shutdownRecorder("org.test.first.v1"),
	})))
	assert.NilError(t, registerExecutable(b, extensions.New(extensions.Declaration{
		ID: "org.test.boom.v1",
		Init: func(context.Context, extensions.Config, extensions.Resolver) error {
			return errors.New("init failed")
		},
		Shutdown: shutdownRecorder("org.test.boom.v1"),
	})))
	assert.NilError(t, registerExecutable(b, extensions.New(extensions.Declaration{
		ID:       "org.test.last.v1",
		Init:     func(context.Context, extensions.Config, extensions.Resolver) error { return nil },
		Shutdown: shutdownRecorder("org.test.last.v1"),
	})))

	err := b.Init(t.Context(), nil)
	assert.ErrorContains(t, err, "init failed")

	assert.NilError(t, b.Shutdown(t.Context()))
	assert.DeepEqual(t, order, []extensions.ExtensionID{"org.test.first.v1"})
}

func TestLookupProviders(t *testing.T) {
	b := New()
	first := pingProvider{}
	second := pingProvider{}

	for _, ext := range []extensions.Declaration{
		{ID: "org.test.first.v1", Providers: []extensions.Provider{{Point: "point", Impl: first}}},
		{ID: "org.test.second.v1", Providers: []extensions.Provider{{Point: "point", Impl: second}}},
	} {
		assert.NilError(t, registerExecutable(b, extensions.New(ext)))
	}

	provider, err := b.Provider("point", "org.test.second.v1")
	assert.NilError(t, err)
	assert.Equal(t, provider, second)
	providers := b.Providers("point")
	assert.Equal(t, len(providers), 2)
	providerIDs := map[extensions.ExtensionID]bool{}
	for _, provider := range providers {
		providerIDs[provider.Identity.ID] = true
	}
	assert.Check(t, providerIDs["org.test.first.v1"])
	assert.Check(t, providerIDs["org.test.second.v1"])
}

// TestConcurrentAccess exercises concurrent reads and registration.
func TestConcurrentAccess(t *testing.T) {
	b := New()
	assert.NilError(t, registerExecutable(b, extensions.New(extensions.Declaration{
		ID:        "org.test.a.v1",
		Providers: []extensions.Provider{{Point: "a.point.v1", Impl: pingProvider{}}},
	})))
	assert.NilError(t, b.Init(t.Context(), nil))

	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			_ = b.Providers("a.point.v1")
			_, _ = b.Provider("a.point.v1", "org.test.a.v1")
		})
	}
	wg.Go(func() {
		_ = registerExecutable(b, extensions.New(extensions.Declaration{ID: "org.test.b.v1"}))
	})
	wg.Wait()
}

func TestTypedPointLookup(t *testing.T) {
	point := extensions.DefinePoint[interface{ Ping(context.Context) error }]("test.typed.v1")
	b := New()
	first := pingProvider{}
	second := pingProvider{}
	assert.NilError(t, registerExecutable(b, extensions.New(extensions.Declaration{ID: "org.test.first.v1", Providers: []extensions.Provider{point.Provide(first)}})))
	assert.NilError(t, registerExecutable(b, extensions.New(extensions.Declaration{ID: "org.test.second.v1", Providers: []extensions.Provider{point.Provide(second)}})))

	providers, err := point.All(b)
	assert.NilError(t, err)
	assert.Equal(t, len(providers), 2)
	assert.DeepEqual(t, providers[0].Identity, extensions.ExtensionIdentity{
		ID: "org.test.first.v1",
		Origin: extensions.ExtensionOrigin{
			Kind:       extensions.ExtensionOriginExecutable,
			Executable: &extensions.ExecutableOrigin{Path: "test-executable"},
		},
	})
	assert.Equal(t, providers[0].Impl, first)

	provider, err := point.ByExtension(b, "org.test.second.v1")
	assert.NilError(t, err)
	assert.Equal(t, provider, second)

	assert.Equal(t, point.Dependency(), extensions.Dependency{Point: point.ID()})
}

func TestTypedPointLookupRejectsWrongImplementationType(t *testing.T) {
	point := extensions.DefinePoint[interface{ Ping(context.Context) error }]("test.typed.v1")
	b := New()
	assert.NilError(t, registerExecutable(b, extensions.New(extensions.Declaration{ID: "org.test.broken.v1", Providers: []extensions.Provider{{Point: point.ID(), Impl: "not a ping provider"}}})))

	_, err := point.All(b)
	assert.ErrorContains(t, err, `extension "org.test.broken.v1" provider for point "test.typed.v1" has type string`)
}

func TestRegisterRejectsExtensionConflicts(t *testing.T) {
	for _, tc := range []struct {
		name    string
		first   []extensions.ExtensionID
		second  []extensions.ExtensionID
		wantErr string
	}{
		{
			name:    "first extension declares conflict",
			first:   []extensions.ExtensionID{"org.test.second.v1"},
			wantErr: `extension "org.test.second.v1" conflicts with extension "org.test.first.v1"`,
		},
		{
			name:    "second extension declares conflict",
			second:  []extensions.ExtensionID{"org.test.first.v1"},
			wantErr: `extension "org.test.second.v1" conflicts with extension "org.test.first.v1"`,
		},
		{
			name:    "both extensions declare conflict",
			first:   []extensions.ExtensionID{"org.test.second.v1"},
			second:  []extensions.ExtensionID{"org.test.first.v1"},
			wantErr: `extension "org.test.second.v1" conflicts with extension "org.test.first.v1"`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := New()
			assert.NilError(t, registerExecutable(b, extensions.New(extensions.Declaration{ID: "org.test.first.v1", Conflicts: tc.first})))

			err := registerExecutable(b, extensions.New(extensions.Declaration{ID: "org.test.second.v1", Conflicts: tc.second}))
			assert.ErrorContains(t, err, tc.wantErr)
		})
	}
}

func TestRegisterRejectsInvalidExtensionConflicts(t *testing.T) {
	for _, tc := range []struct {
		name      string
		conflicts []extensions.ExtensionID
		wantErr   string
	}{
		{
			name:      "empty conflict id",
			conflicts: []extensions.ExtensionID{""},
			wantErr:   `extension "org.test.invalid.v1" has empty conflict id`,
		},
		{
			name:      "self conflict",
			conflicts: []extensions.ExtensionID{"org.test.invalid.v1"},
			wantErr:   `extension "org.test.invalid.v1" conflicts with itself`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := New()
			err := registerExecutable(b, extensions.New(extensions.Declaration{ID: "org.test.invalid.v1", Conflicts: tc.conflicts}))
			assert.ErrorContains(t, err, tc.wantErr)
		})
	}
}

func TestInitFailsForMissingRequiredDependency(t *testing.T) {
	b := New()
	assert.NilError(t, registerExecutable(b, extensions.New(extensions.Declaration{ID: "org.test.dependent.v1", Dependencies: []extensions.Dependency{{Point: "missing.point"}}})))

	err := b.Init(t.Context(), nil)
	assert.ErrorContains(t, err, `requires missing point "missing.point"`)
}

func TestInitAllowsMissingOptionalDependency(t *testing.T) {
	b := New()
	initialized := false
	err := registerExecutable(b, extensions.New(extensions.Declaration{
		ID:           "org.test.dependent.v1",
		Dependencies: []extensions.Dependency{{Point: "missing.point", Optional: true}},
		Init: func(context.Context, extensions.Config, extensions.Resolver) error {
			initialized = true
			return nil
		},
	}))
	assert.NilError(t, err)

	assert.NilError(t, b.Init(t.Context(), nil))
	assert.Check(t, initialized)
}

func TestInitFailsForDependencyCycle(t *testing.T) {
	b := New()
	for _, ext := range []extensions.Declaration{
		{ID: "org.test.first.v1", Dependencies: []extensions.Dependency{{Extension: "org.test.second.v1"}}},
		{ID: "org.test.second.v1", Dependencies: []extensions.Dependency{{Extension: "org.test.first.v1"}}},
	} {
		assert.NilError(t, registerExecutable(b, extensions.New(ext)))
	}

	err := b.Init(t.Context(), nil)
	assert.ErrorContains(t, err, "extension dependency cycle")
}

func TestInitWrapsExtensionError(t *testing.T) {
	b := New()
	initErr := errors.New("org.test.boom.v1")
	err := registerExecutable(b, extensions.New(extensions.Declaration{
		ID: "org.test.broken.v1",
		Init: func(context.Context, extensions.Config, extensions.Resolver) error {
			return initErr
		},
	}))
	assert.NilError(t, err)

	err = b.Init(t.Context(), nil)
	assert.ErrorIs(t, err, initErr)
}

func TestProviderIdentity(t *testing.T) {
	const point = extensions.PointID("org.example.decider.v1")
	stock := "stock"
	custom := "custom"

	stockExt := extensions.New(extensions.Declaration{
		ID:        "org.mobyproject.stock.v1",
		Providers: []extensions.Provider{{Point: point, Impl: stock}},
	})
	customExt := extensions.New(extensions.Declaration{
		ID:        "org.example.custom.v1",
		Providers: []extensions.Provider{{Point: point, Impl: custom}},
	})
	stockIdentity := extensions.ExtensionIdentity{
		ID:     "org.mobyproject.stock.v1",
		Origin: extensions.ExtensionOrigin{Kind: extensions.ExtensionOriginBuiltin},
	}
	customIdentity := extensions.ExtensionIdentity{
		ID: "org.example.custom.v1",
		Origin: extensions.ExtensionOrigin{
			Kind:       extensions.ExtensionOriginExecutable,
			Executable: &extensions.ExecutableOrigin{Path: "test-executable"},
		},
	}

	t.Run("origin is recorded at registration", func(t *testing.T) {
		b := New()
		assert.NilError(t, b.Register(stockIdentity, stockExt))
		providers := b.Providers(point)
		assert.Equal(t, len(providers), 1)
		assert.Equal(t, providers[0].Impl, stock)
		assert.Equal(t, providers[0].Identity, stockIdentity)
	})

	t.Run("builtins are enumerated beside executable providers", func(t *testing.T) {
		b := New()
		assert.NilError(t, b.Register(stockIdentity, stockExt))
		assert.NilError(t, b.Register(customIdentity, customExt))
		providers := b.Providers(point)
		assert.Equal(t, len(providers), 2, "the registry enumerates; it does not mask")

		effective := extensions.EffectiveProviders(providers)
		assert.Equal(t, len(effective), 1, "origin precedence is selection, applied over the enumeration")
		assert.Equal(t, effective[0].Identity, customIdentity)
	})

	t.Run("a masked builtin stays reachable by id", func(t *testing.T) {
		b := New()
		assert.NilError(t, b.Register(stockIdentity, stockExt))
		assert.NilError(t, b.Register(customIdentity, customExt))
		impl, err := b.Provider(point, "org.mobyproject.stock.v1")
		assert.NilError(t, err)
		assert.Equal(t, impl, stock)
	})
}

func TestRegisterRejectsInvalidIdentityWithoutMutation(t *testing.T) {
	ext := extensions.New(extensions.Declaration{ID: "org.example.valid.v1"})
	for _, tc := range []struct {
		name     string
		identity extensions.ExtensionIdentity
		wantErr  string
	}{
		{name: "empty origin", identity: extensions.ExtensionIdentity{ID: "org.example.valid.v1"}, wantErr: "extension origin kind is required"},
		{name: "unknown origin", identity: extensions.ExtensionIdentity{ID: "org.example.valid.v1", Origin: extensions.ExtensionOrigin{Kind: "remote"}}, wantErr: `invalid extension origin kind "remote"`},
		{name: "invalid id", identity: extensions.ExtensionIdentity{ID: "invalid", Origin: extensions.ExtensionOrigin{Kind: extensions.ExtensionOriginExecutable, Executable: &extensions.ExecutableOrigin{Path: "test-executable"}}}, wantErr: `invalid extension id "invalid"`},
		{name: "declaration mismatch", identity: extensions.ExtensionIdentity{ID: "org.example.other.v1", Origin: extensions.ExtensionOrigin{Kind: extensions.ExtensionOriginExecutable, Executable: &extensions.ExecutableOrigin{Path: "test-executable"}}}, wantErr: `identity id "org.example.other.v1" does not match declared id "org.example.valid.v1"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := New()
			err := b.Register(tc.identity, ext)
			assert.ErrorContains(t, err, tc.wantErr)
			assert.NilError(t, registerExecutable(b, ext), "rejected identity mutated broker state")
		})
	}
}

func TestRegisterRejectsDuplicateIdentity(t *testing.T) {
	b := New()
	ext := extensions.New(extensions.Declaration{ID: "org.example.duplicate.v1"})
	assert.NilError(t, registerExecutable(b, ext))
	err := registerExecutable(b, ext)
	assert.ErrorContains(t, err, `extension "org.example.duplicate.v1" is already registered`)
}
