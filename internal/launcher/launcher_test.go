// SPDX-FileCopyrightText: Copyright The Moby Authors
// SPDX-License-Identifier: Apache-2.0

package launcher

import (
	"bufio"
	"context"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/containerd/log"
	"github.com/moby/extensions"
	echov1 "github.com/moby/extensions/internal/launcher/echo/v1"
	echopb "github.com/moby/extensions/internal/launcher/echo/v1/protogen"
	"github.com/moby/extensions/sdk/sdkapi"
	sdkapipb "github.com/moby/extensions/sdk/sdkapi/protogen"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"gotest.tools/v3/assert"
	is "gotest.tools/v3/assert/cmp"
)

type messageHook struct {
	messages []string
}

func (*messageHook) Levels() []logrus.Level {
	return logrus.AllLevels
}

func (h *messageHook) Fire(entry *logrus.Entry) error {
	h.messages = append(h.messages, entry.Message)
	return nil
}

type initializeContextServer struct {
	hasDeadline chan bool
}

func (initializeContextServer) Describe(context.Context, *sdkapi.DescribeRequest) (*sdkapi.DescribeResponse, error) {
	return &sdkapi.DescribeResponse{}, nil
}

func (s initializeContextServer) Initialize(ctx context.Context, _ *sdkapi.InitializeRequest) (*sdkapi.InitializeResponse, error) {
	_, hasDeadline := ctx.Deadline()
	s.hasDeadline <- hasDeadline
	return &sdkapi.InitializeResponse{}, nil
}

func TestLaunchedInitializeUsesCallerContext(t *testing.T) {
	t.Parallel()

	listener := bufconn.Listen(1024 * 1024)
	server := grpc.NewServer()
	recorder := &initializeContextServer{hasDeadline: make(chan bool, 1)}
	sdkapipb.RegisterServer(server, recorder)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	conn, err := grpc.NewClient("passthrough:///initialize",
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(func(context.Context, string) (net.Conn, error) {
			return listener.Dial()
		}),
	)
	assert.NilError(t, err)
	t.Cleanup(func() { assert.NilError(t, conn.Close()) })

	launched := &Launched{Conn: conn}
	assert.NilError(t, launched.Initialize(context.Background()))
	assert.Equal(t, <-recorder.hasDeadline, false)
}

func TestLogOutputChunksLongRecords(t *testing.T) {
	t.Parallel()

	hook := &messageHook{}
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	logger.AddHook(hook)
	ctx := log.WithLogger(context.Background(), logrus.NewEntry(logger))

	first := strings.Repeat("a", maxOutputRecordSize-1) + "\r"
	second := strings.Repeat("b", maxOutputRecordSize)
	logOutput(ctx, "test", strings.NewReader(first+second+"tail\r"))

	assert.Check(t, is.DeepEqual(hook.messages, []string{first, second, "tail"}))
	for _, message := range hook.messages {
		assert.Check(t, len(message) <= maxOutputRecordSize, "log record is %d bytes", len(message))
	}
}

func TestLogOutputPreservesLines(t *testing.T) {
	t.Parallel()

	hook := &messageHook{}
	logger := logrus.New()
	logger.SetOutput(io.Discard)
	logger.AddHook(hook)
	ctx := log.WithLogger(context.Background(), logrus.NewEntry(logger))

	logOutput(ctx, "test", strings.NewReader("first\r\nsecond\n\nfinal\r"))

	assert.Check(t, is.DeepEqual(hook.messages, []string{"first", "second", "", "final"}))
}

func TestWaitReadyRejectsOversizedAcknowledgement(t *testing.T) {
	t.Parallel()

	input := strings.Repeat("x", maxOutputRecordSize+1) + "\n"
	reader := bufio.NewReaderSize(strings.NewReader(input), maxOutputRecordSize)
	err := waitReady(context.Background(), io.NopCloser(strings.NewReader("")), reader)
	assert.ErrorContains(t, err, "readiness acknowledgement exceeds 16384 bytes")
}

func exeName(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}

func shortTempDir(t *testing.T) string {
	t.Helper()
	// Keep socket paths relative so they fit Windows' AF_UNIX path limit.
	dir, err := os.MkdirTemp(".", "m")
	assert.NilError(t, err)
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func TestBinaries(t *testing.T) {
	dir := t.TempDir()
	exe := filepath.Join(dir, exeName("org.example.one.v1"))
	assert.NilError(t, os.WriteFile(exe, []byte("x"), 0o755))
	assert.NilError(t, os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("x"), 0o644))
	assert.NilError(t, os.Mkdir(filepath.Join(dir, "sub"), 0o755))

	assert.NilError(t, os.WriteFile(filepath.Join(dir, exeName("helper")), []byte("x"), 0o755))

	if runtime.GOOS == "windows" {
		upper := filepath.Join(dir, "org.example.upper.v1.EXE")
		assert.NilError(t, os.WriteFile(upper, []byte("x"), 0o755))
	}

	bins, err := Binaries(context.Background(), dir)
	assert.NilError(t, err)
	want := []string{exe}
	if runtime.GOOS == "windows" {
		want = append(want, filepath.Join(dir, "org.example.upper.v1.EXE"))
	}
	assert.DeepEqual(t, bins, want)

	missing, err := Binaries(context.Background(), filepath.Join(dir, "does-not-exist"))
	assert.NilError(t, err)
	assert.Check(t, is.Len(missing, 0))
}

// TestBinariesRefusesWorldWritable verifies the root-exec safety filter.
func TestBinariesRefusesWorldWritable(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("world-writable bit is not meaningful on Windows")
	}
	dir := t.TempDir()
	good := filepath.Join(dir, "org.example.good.v1")
	bad := filepath.Join(dir, "org.example.bad.v1")
	assert.NilError(t, os.WriteFile(good, []byte("x"), 0o755))
	assert.NilError(t, os.WriteFile(bad, []byte("x"), 0o755))
	assert.NilError(t, os.Chmod(bad, 0o757)) // o+w

	bins, err := Binaries(context.Background(), dir)
	assert.NilError(t, err)
	assert.DeepEqual(t, bins, []string{good})

	wwDir := t.TempDir()
	assert.NilError(t, os.WriteFile(filepath.Join(wwDir, "org.example.x.v1"), []byte("x"), 0o755))
	assert.NilError(t, os.Chmod(wwDir, 0o777))
	bins, err = Binaries(context.Background(), wwDir)
	assert.NilError(t, err)
	assert.Check(t, is.Len(bins, 0))
}

// TestBinariesRefusesUntrustedOwner verifies the ownership filter.
func TestBinariesRefusesUntrustedOwner(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("ownership is not enforced on Windows")
	}
	if os.Geteuid() != 0 {
		t.Skip("changing a file's owner requires root")
	}
	dir := t.TempDir()
	good := filepath.Join(dir, "org.example.good.v1")
	bad := filepath.Join(dir, "org.example.bad.v1")
	assert.NilError(t, os.WriteFile(good, []byte("x"), 0o755))
	assert.NilError(t, os.WriteFile(bad, []byte("x"), 0o755))
	assert.NilError(t, os.Chown(bad, 65534, 65534)) // nobody: not root, not us

	bins, err := Binaries(context.Background(), dir)
	assert.NilError(t, err)
	assert.DeepEqual(t, bins, []string{good})
}

func TestLaunchOutOfProcess(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and launches a helper binary")
	}
	id := "org.example." + strings.Repeat("long.", 16) + "exthook.v1"
	bin := filepath.Join(t.TempDir(), exeName(id))
	build := exec.Command("go", "build", "-ldflags", "-X main.extensionID="+id, "-o", bin, "./testdata/exthook")
	out, err := build.CombinedOutput()
	assert.NilError(t, err, "build extension: %s", out)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	runtimeDir := filepath.Join(shortTempDir(t), strings.Repeat("r", 40))
	assert.NilError(t, os.Mkdir(runtimeDir, 0o755))
	launched, err := Launcher{RuntimeDir: runtimeDir}.Launch(ctx, bin)
	assert.NilError(t, err)
	defer func() { assert.NilError(t, launched.Close(context.Background())) }()

	assert.Equal(t, launched.ID, extensions.ExtensionID(id))
	assert.Equal(t, launched.Path, bin)
	assert.Check(t, is.Len(launched.Points, 2))
	assert.Equal(t, launched.Points[0].ID, echov1.Point.ID())
	assert.DeepEqual(t, launched.OfferedPoints, []extensions.PointID{echov1.Point.ID()})
	assert.DeepEqual(t, launched.ProviderServices[echov1.Point.ID()], []string{"moby.extensions.internal.launcher.echo.v1.Echo"})

	client := echopb.ClientProvider(launched.Conn).Impl.(echov1.Echo)

	resp, err := client.Echo(ctx, &echov1.EchoRequest{Message: "ping"})
	assert.NilError(t, err, "non-empty message should be echoed")
	assert.Equal(t, resp.Message, "ping")

	_, err = client.Echo(ctx, &echov1.EchoRequest{})
	assert.Check(t, is.ErrorContains(err, "message must not be empty"))
}

func TestStopProcessSignalledExitIsNotAnError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no signal semantics to assert on Windows")
	}
	cmd := exec.Command("sleep", "60")
	lifetime, wait, err := startProcess(cmd)
	assert.NilError(t, err)
	defer func() { assert.NilError(t, lifetime.Close()) }()

	assert.NilError(t, stopProcess(context.Background(), cmd, wait, 5*time.Second))
}

func TestStopProcessAfterSelfExit(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("zombies are a unix concept")
	}
	cmd := exec.Command("sleep", "0.05")
	lifetime, wait, err := startProcess(cmd)
	assert.NilError(t, err)
	defer func() { assert.NilError(t, lifetime.Close()) }()
	time.Sleep(500 * time.Millisecond) // let it exit and be reaped

	done := make(chan error, 1)
	go func() { done <- stopProcess(context.Background(), cmd, wait, time.Second) }()
	select {
	case err := <-done:
		assert.NilError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("stopProcess blocked: the child was never reaped")
	}
}

func TestServicesRequireDeclaringThePoint(t *testing.T) {
	const declared = extensions.PointID("org.mobyproject.extension.declared.v1")
	const undeclared = extensions.PointID("org.example.undeclared.v1")

	l := &Launched{
		ID:     "org.example.ext.v1",
		Points: []LaunchedPoint{{ID: declared}},
		ProviderServices: map[extensions.PointID][]string{
			undeclared: {"example.Service"},
		},
	}
	assert.ErrorContains(t, validateDeclaredServices("org.example.ext.v1", l),
		"without declaring it")

	l.ProviderServices = map[extensions.PointID][]string{declared: {"example.Service"}}
	assert.NilError(t, validateDeclaredServices("org.example.ext.v1", l))
}
