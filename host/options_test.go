// SPDX-FileCopyrightText: Copyright The Moby Authors
// SPDX-License-Identifier: Apache-2.0

package host_test

import (
	"context"
	"testing"

	"github.com/moby/extensions"
	"github.com/moby/extensions/host"
	"gotest.tools/v3/assert"
)

func TestNewWithNoOptions(t *testing.T) {
	t.Parallel()

	h, err := host.New(t.Context())
	assert.NilError(t, err)
	assert.NilError(t, h.Shutdown(t.Context()))
}

func TestNewRejectsNilOption(t *testing.T) {
	t.Parallel()

	var option host.Option
	h, err := host.New(t.Context(), option)
	assert.Assert(t, h == nil)
	if err == nil {
		t.Fatal("host.New returned nil error for a nil option")
	}
	assert.Equal(t, err.Error(), "nil host option")
}

func TestRepeatedExtensionOptionsComposeAndCopyInput(t *testing.T) {
	t.Parallel()

	point := extensions.DefinePoint[any]("org.example.option.v1")
	first := extensions.New(extensions.Declaration{
		ID:        "org.example.first.v1",
		Providers: []extensions.Provider{point.Provide("first")},
	})
	second := extensions.New(extensions.Declaration{
		ID:        "org.example.second.v1",
		Providers: []extensions.Provider{point.Provide("second")},
	})

	exts := []extensions.Extension{first}
	firstOption := host.WithExtensions(exts...)
	exts[0] = second

	h, err := host.New(t.Context(), firstOption, host.WithExtensions(second))
	assert.NilError(t, err)
	defer func() { assert.NilError(t, h.Shutdown(context.WithoutCancel(t.Context()))) }()

	got, err := h.Provider(point.ID(), "org.example.first.v1")
	assert.NilError(t, err)
	assert.Equal(t, got, "first")
	got, err = h.Provider(point.ID(), "org.example.second.v1")
	assert.NilError(t, err)
	assert.Equal(t, got, "second")
}
