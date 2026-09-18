// SPDX-FileCopyrightText: Copyright The Moby Authors
// SPDX-License-Identifier: Apache-2.0

package testutils

import (
	"testing"

	"github.com/moby/extensions"
	"gotest.tools/v3/assert"
)

func TestStaticResolverScopesProvidersToPoint(t *testing.T) {
	point := extensions.DefinePoint[any]("org.example.point.v1")
	other := extensions.DefinePoint[any]("org.example.other.v1")
	resolver := StaticResolver{
		Provide(point, "provider"),
		Provide(other, "other provider"),
	}

	assert.Equal(t, len(resolver.Providers(point.ID())), 1)
	assert.Equal(t, len(resolver.Providers(other.ID())), 1)

	provider, err := resolver.Provider(other.ID(), "org.example.test1.v1")
	assert.NilError(t, err)
	assert.Equal(t, provider, "other provider")

	_, err = resolver.Provider(point.ID(), "org.example.test2.v1")
	assert.ErrorContains(t, err, `no provider for point "org.example.point.v1"`)
}
