// SPDX-FileCopyrightText: Copyright The Moby Authors
// SPDX-License-Identifier: Apache-2.0

package extensiondecl

import (
	"testing"

	"github.com/moby/extensions"
	"github.com/moby/extensions/sdk/sdkapi"
	"gotest.tools/v3/assert"
	is "gotest.tools/v3/assert/cmp"
)

func TestParseRejectsNilAndEmptyDeclarations(t *testing.T) {
	for _, test := range []struct {
		name string
		decl *sdkapi.Declaration
	}{
		{name: "nil"},
		{name: "empty", decl: &sdkapi.Declaration{}},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := Parse("example", test.decl)
			assert.Error(t, err, `extension "example" described no extension`)
		})
	}
}

func TestParseRejectsIDMismatch(t *testing.T) {
	_, err := Parse("example", &sdkapi.Declaration{ID: "other"})
	assert.Error(t, err, `extension "example" declared id "other", which must match its file name`)
}

func TestParseConvertsDeclaration(t *testing.T) {
	decl, err := Parse("example", &sdkapi.Declaration{
		ID: "example",
		Providers: []sdkapi.PointDeclaration{
			{ID: "org.example.one.v1"},
			{ID: "org.example.two.v1"},
		},
		Dependencies: []sdkapi.Dependency{
			{Point: "org.example.one.v1", Extension: "provider", Optional: true},
		},
		Conflicts: []string{"conflict"},
		ProviderServices: []sdkapi.ProviderServices{
			{Point: "org.example.one.v1", Services: []string{"service.One"}},
			{Point: "org.example.one.v1", Services: []string{"service.Two"}},
		},
		OfferedPoints: []string{"org.example.one.v1"},
	})
	assert.NilError(t, err)
	assert.Assert(t, is.DeepEqual(decl, &Declaration{
		ID:            extensions.ExtensionID("example"),
		Points:        []extensions.PointID{"org.example.one.v1", "org.example.two.v1"},
		OfferedPoints: []extensions.PointID{"org.example.one.v1"},
		Dependencies:  []extensions.Dependency{{Point: "org.example.one.v1", Extension: "provider", Optional: true}},
		Conflicts:     []extensions.ExtensionID{"conflict"},
		ProviderServices: map[extensions.PointID][]string{
			"org.example.one.v1": {"service.One", "service.Two"},
		},
	}))
}

func TestParseRejectsInvalidOffers(t *testing.T) {
	const point = "org.example.api.v1"
	const metadata = "org.mobyproject.extension.service.v0"
	tests := []struct {
		name      string
		providers []sdkapi.PointDeclaration
		offers    []string
		services  []sdkapi.ProviderServices
		want      string
	}{
		{name: "invalid", offers: []string{"not-versioned"}, want: "offered an invalid point"},
		{name: "duplicate", providers: []sdkapi.PointDeclaration{{ID: point}}, offers: []string{point, point}, services: []sdkapi.ProviderServices{{Point: point, Services: []string{"example.API"}}}, want: "more than once"},
		{name: "unimplemented", offers: []string{point}, want: "without implementing it"},
		{name: "metadata point", providers: []sdkapi.PointDeclaration{{ID: metadata}}, offers: []string{metadata}, services: []sdkapi.ProviderServices{{Point: metadata, Services: []string{"example.Metadata"}}}, want: "cannot offer publication metadata"},
		{name: "missing service", providers: []sdkapi.PointDeclaration{{ID: point}}, offers: []string{point}, want: "without reporting a service"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Parse("example", &sdkapi.Declaration{
				ID:               "example",
				Providers:        tc.providers,
				OfferedPoints:    tc.offers,
				ProviderServices: tc.services,
			})
			assert.ErrorContains(t, err, tc.want)
		})
	}
}

func TestParseRejectsServicesForUndeclaredPoint(t *testing.T) {
	_, err := Parse("example", &sdkapi.Declaration{
		ID: "example",
		ProviderServices: []sdkapi.ProviderServices{
			{Point: "point.one", Services: []string{"service.One"}},
		},
	})
	assert.Error(t, err, `extension "example" serves services for point "point.one" without declaring it`)
}

func TestServicesSkipsEmptyGroups(t *testing.T) {
	assert.Assert(t, is.DeepEqual(Services([]sdkapi.ProviderServices{
		{Point: "", Services: []string{"ignored"}},
		{Point: "point.one"},
	}), map[extensions.PointID][]string{}))
}
