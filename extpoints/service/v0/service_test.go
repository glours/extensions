// SPDX-FileCopyrightText: Copyright The Moby Authors
// SPDX-License-Identifier: Apache-2.0

package servicev0

import (
	"fmt"
	"testing"

	"github.com/moby/extensions"
	"gotest.tools/v3/assert"
)

func TestOfferCopiesAndReportsPoints(t *testing.T) {
	foo := extensions.DefinePoint[interface{ Foo() }]("org.example.foo.v1")
	bar := extensions.DefinePoint[interface{ Bar() }]("org.example.bar.v1")
	points := []offeredPoint{foo, bar}
	offer := Offer(points...)
	points[0] = extensions.DefinePoint[any]("org.example.changed.v1")

	assert.Equal(t, offer.Point, Point.ID())
	assert.Assert(t, extensions.IsMetadataPoint(Point.ID()),
		"the offer marker point must be classified as framework metadata")
	provider := offer.Impl.(Provider)
	got := provider.OfferedPoints()
	assert.DeepEqual(t, got, []extensions.PointID{foo.ID(), bar.ID()})
	got[0] = "org.example.changed.v1"
	assert.DeepEqual(t, provider.OfferedPoints(), []extensions.PointID{foo.ID(), bar.ID()})
}

func TestOfferInvariants(t *testing.T) {
	foo := extensions.DefinePoint[any]("org.example.foo.v1")
	cases := []struct {
		name string
		call func()
		want string
	}{
		{name: "no points", call: func() { Offer() }, want: "servicev0: no offered points"},
		{name: "metadata point", call: func() { Offer(Point) }, want: fmt.Sprintf("servicev0: point %q cannot offer itself", Point.ID())},
		{name: "duplicate point", call: func() { Offer(foo, foo) }, want: `servicev0: duplicate point "org.example.foo.v1"`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assertPanic(t, tc.want, tc.call)
		})
	}
}

func assertPanic(t *testing.T, want string, call func()) {
	t.Helper()
	defer func() {
		got := recover()
		if got == nil {
			t.Fatal("expected panic")
		}
		assert.Equal(t, fmt.Sprint(got), want)
	}()
	call()
}
