// SPDX-FileCopyrightText: Copyright The Moby Authors
// SPDX-License-Identifier: Apache-2.0

package dependencyresolver

import (
	"context"
	"testing"

	"github.com/moby/extensions"
	"github.com/moby/extensions/clientpoint"
	"google.golang.org/grpc"
	"gotest.tools/v3/assert"
)

type testConn struct{}

func (testConn) Invoke(context.Context, string, any, any, ...grpc.CallOption) error {
	return nil
}

func (testConn) NewStream(context.Context, *grpc.StreamDesc, string, ...grpc.CallOption) (grpc.ClientStream, error) {
	return nil, nil
}

func TestResolverMissingClient(t *testing.T) {
	point := extensions.PointID("org.example.point.v1")
	resolver := New(testConn{}, nil)

	_, err := resolver.Provider(point, "ignored")
	assert.ErrorContains(t, err, `extension has no resolvable dependency for point "org.example.point.v1" (declare it with Depends)`)
	assert.Assert(t, resolver.Providers(point) == nil)
}

func TestResolverNilConnection(t *testing.T) {
	point := extensions.PointID("org.example.point.v1")
	resolver := New(nil, map[extensions.PointID]clientpoint.Provider{
		point: func(grpc.ClientConnInterface) extensions.Provider {
			return extensions.Provider{Point: point, Impl: "provider"}
		},
	})

	_, err := resolver.Provider(point, "ignored")
	assert.ErrorContains(t, err, `extension has no resolvable dependency for point "org.example.point.v1" (declare it with Depends)`)
}

func TestResolverProviderIgnoresExtensionID(t *testing.T) {
	point := extensions.PointID("org.example.point.v1")
	conn := testConn{}
	resolver := New(conn, map[extensions.PointID]clientpoint.Provider{
		point: func(got grpc.ClientConnInterface) extensions.Provider {
			assert.Equal(t, got, conn)
			return extensions.Provider{Point: point, Impl: "provider"}
		},
	})

	impl, err := resolver.Provider(point, "not-the-provider-id")
	assert.NilError(t, err)
	assert.Equal(t, impl, "provider")
}

func TestResolverProvidersReturnsSingleProvider(t *testing.T) {
	point := extensions.PointID("org.example.point.v1")
	resolver := New(testConn{}, map[extensions.PointID]clientpoint.Provider{
		point: func(grpc.ClientConnInterface) extensions.Provider {
			return extensions.Provider{Point: point, Impl: "provider"}
		},
	})

	assert.DeepEqual(t, resolver.Providers(point), []extensions.ResolvedProvider{{Impl: "provider"}})
}

func TestResolverBuildsGeneratedProvider(t *testing.T) {
	point := extensions.PointID("org.example.point.v1")
	conn := testConn{}
	resolver := New(conn, map[extensions.PointID]clientpoint.Provider{
		point: generatedProvider(t, point, conn),
	})

	impl, err := resolver.Provider(point, "ignored")
	assert.NilError(t, err)
	assert.Equal(t, impl, "generated provider")
}

func generatedProvider(t *testing.T, point extensions.PointID, want grpc.ClientConnInterface) clientpoint.Provider {
	return func(conn grpc.ClientConnInterface) extensions.Provider {
		assert.Equal(t, conn, want)
		return extensions.Provider{Point: point, Impl: "generated provider"}
	}
}
