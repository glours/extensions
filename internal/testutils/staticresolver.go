// SPDX-FileCopyrightText: Copyright The Moby Authors
// SPDX-License-Identifier: Apache-2.0

// Package testutils provides test doubles for the extension framework.
package testutils

import (
	"fmt"

	"github.com/moby/extensions"
)

// StaticResolver answers from a fixed set of point providers.
type StaticResolver []StaticProvider

// StaticProvider is one provider entry in a StaticResolver.
type StaticProvider struct {
	Point    extensions.PointID
	Identity extensions.ExtensionIdentity
	Impl     any
}

// Provide returns a static provider entry for point.
func Provide[T any](point extensions.Point[T], impl any) StaticProvider {
	return StaticProvider{Point: point.ID(), Impl: impl}
}

// Provider implements [extensions.Resolver].
func (r StaticResolver) Provider(point extensions.PointID, id extensions.ExtensionID) (any, error) {
	for _, p := range r.Providers(point) {
		if p.Identity.ID == id {
			return p.Impl, nil
		}
	}
	return nil, fmt.Errorf("no provider for point %q from extension %q", point, id)
}

// Providers implements [extensions.Resolver].
func (r StaticResolver) Providers(point extensions.PointID) []extensions.ResolvedProvider {
	var providers []extensions.ResolvedProvider
	for _, provider := range r {
		if provider.Point != point {
			continue
		}
		identity := provider.Identity
		id := identity.ID
		if id == "" {
			id = extensions.ExtensionID(fmt.Sprintf("org.example.test%d.v1", len(providers)+1))
		}
		if identity.Origin.Kind == "" {
			identity.Origin = extensions.ExtensionOrigin{
				Kind:       extensions.ExtensionOriginExecutable,
				Executable: &extensions.ExecutableOrigin{Path: "test-executable"},
			}
		}
		identity.ID = id
		providers = append(providers, extensions.ResolvedProvider{
			Identity: identity,
			Impl:     provider.Impl,
		})
	}
	return providers
}
