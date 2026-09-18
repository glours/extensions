// SPDX-FileCopyrightText: Copyright The Moby Authors
// SPDX-License-Identifier: Apache-2.0

package host_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moby/extensions"
	greeterv0 "github.com/moby/extensions/example/greeter/v0"
	greeterpb "github.com/moby/extensions/example/greeter/v0/protogen"
	"github.com/moby/extensions/host"
	"github.com/moby/extensions/testdata/greeterdep"
	"gotest.tools/v3/assert"
)

type recordingGreeter struct{ calls *atomic.Int32 }

func extensionBinaryPath(dir, id string) string {
	if runtime.GOOS == "windows" {
		id += ".exe"
	}
	return filepath.Join(dir, id)
}

func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp(".", "m") //nolint:usetesting // Keep socket paths relative so they fit Windows' AF_UNIX path limit.
	assert.NilError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func (g recordingGreeter) Greet(_ context.Context, req *greeterv0.HelloRequest) (*greeterv0.HelloReply, error) {
	g.calls.Add(1)
	return &greeterv0.HelloReply{Message: "hello " + req.Name}, nil
}

func TestOutOfProcessDependency(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and launches a helper binary")
	}

	dir := t.TempDir()
	bin := extensionBinaryPath(dir, greeterdep.ID)
	build := exec.Command("go", "build", "-o", bin,
		"github.com/moby/extensions/testdata/greeterdep/cmd/greeterdep")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build greeterdep extension: %v\n%s", err, out)
	}

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	var calls atomic.Int32
	greeter := extensions.New(extensions.Declaration{
		ID:        "org.mobyproject.example.greeter-provider.v1",
		Providers: []extensions.Provider{greeterv0.Point.Provide(recordingGreeter{calls: &calls})},
	})

	h, err := host.New(ctx,
		host.WithRuntimeDir(shortTempDir(t)),
		host.WithExtensions(greeter),
		host.WithDirs(dir),
		host.WithDependencyProviders(greeterpb.ServerPoint),
	)
	assert.NilError(t, err)
	defer func() { assert.NilError(t, h.Shutdown(context.WithoutCancel(ctx))) }()

	assert.Equal(t, calls.Load(), int32(1),
		"in-process greeter provider was not called by the out-of-process extension")
}
