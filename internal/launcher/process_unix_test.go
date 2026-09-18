// SPDX-FileCopyrightText: Copyright The Moby Authors
// SPDX-License-Identifier: Apache-2.0

//go:build unix

package launcher

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/moby/extensions"
	"gotest.tools/v3/assert"
)

const helperModeEnv = "MOBY_EXTENSIONS_LAUNCHER_HELPER_MODE"

var helperStatusFile *os.File

func TestMain(m *testing.M) {
	if mode := os.Getenv(helperModeEnv); mode != "" {
		runLauncherHelper(mode)
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func runLauncherHelper(mode string) {
	switch mode {
	case "do-not-read-stdin":
		stopped := make(chan os.Signal, 1)
		signal.Notify(stopped, syscall.SIGTERM)
		<-stopped
	case "process-group-leader":
		helperProcessGroupLeader()
	case "process-group-descendant":
		signal.Ignore(syscall.SIGTERM)
		helperStatusFile = os.NewFile(3, "process-group-status")
		_, _ = fmt.Fprintf(helperStatusFile, "%d\n", os.Getpid())
		waitForever()
	case "pdeathsig-parent":
		helperPdeathsigParent()
	case "pdeathsig-child":
		waitForever()
	default:
		_, _ = fmt.Fprintf(os.Stderr, "unknown launcher helper mode %q\n", mode)
		os.Exit(2)
	}
}

func helperProcessGroupLeader() {
	status := os.NewFile(3, "process-group-status")
	cmd := exec.Command(os.Args[0])
	cmd.Env = launcherHelperEnv("process-group-descendant")
	cmd.ExtraFiles = []*os.File{status}
	if err := cmd.Start(); err != nil {
		_, _ = fmt.Fprintf(status, "error: %v\n", err)
		return
	}
	_ = status.Close()
	_ = cmd.Wait()
}

func helperPdeathsigParent() {
	helpStatus := os.NewFile(3, "pdeathsig-status")
	cmd := exec.CommandContext(context.Background(), os.Args[0])
	cmd.Env = launcherHelperEnv("pdeathsig-child")
	cmd.ExtraFiles = []*os.File{helpStatus}
	_, _, err := startProcess(cmd)
	if err != nil {
		_, _ = fmt.Fprintf(helpStatus, "error: %v\n", err)
		return
	}

	_, _ = fmt.Fprintf(helpStatus, "%d\n", cmd.Process.Pid)
	os.Exit(0)
}

func launcherHelperEnv(mode string) []string {
	prefix := helperModeEnv + "="
	env := make([]string, 0, len(os.Environ())+1)
	for _, value := range os.Environ() {
		if !strings.HasPrefix(value, prefix) {
			env = append(env, value)
		}
	}
	return append(env, prefix+mode)
}

func waitForever() {
	for {
		time.Sleep(time.Hour)
	}
}

func TestLaunchStartupWriteTimeout(t *testing.T) {
	t.Setenv(helperModeEnv, "do-not-read-stdin")

	executable, err := os.Executable()
	assert.NilError(t, err)
	bin := filepath.Join(t.TempDir(), "blocked-extension")
	assert.NilError(t, os.Symlink(executable, bin))

	const readyTimeout = time.Second
	launcher := Launcher{
		RuntimeDir:      t.TempDir(),
		ReadyTimeout:    readyTimeout,
		ShutdownTimeout: time.Second,
		ExtensionConfig: map[extensions.ExtensionID]extensions.Config{
			"blocked-extension": {"payload": strings.Repeat("x", 8*1024*1024)},
		},
	}
	started := time.Now()
	_, err = launcher.Launch(t.Context(), bin)
	assert.ErrorContains(t, err, `write startup config for extension "blocked-extension": context deadline exceeded`)
	assert.Check(t, time.Since(started) < 5*time.Second, "timed-out launch took %s", time.Since(started))
}

func TestStopProcessKillsProcessGroupDescendant(t *testing.T) {
	statusR, statusW, err := os.Pipe()
	assert.NilError(t, err)
	defer func() { assert.NilError(t, statusR.Close()) }()

	cmd := exec.CommandContext(t.Context(), os.Args[0])
	cmd.Env = launcherHelperEnv("process-group-leader")
	cmd.ExtraFiles = []*os.File{statusW}
	lifetime, wait, err := startProcess(cmd)
	assert.NilError(t, err)
	t.Cleanup(func() {
		_ = killProcess(cmd)
		_ = lifetime.Close()
	})
	_ = statusW.Close()

	reader := bufio.NewReader(statusR)
	assert.NilError(t, statusR.SetReadDeadline(time.Now().Add(5*time.Second)))
	line, err := reader.ReadString('\n')
	assert.NilError(t, err)
	descendantPID, err := strconv.Atoi(strings.TrimSpace(line))
	assert.NilError(t, err, line)
	defer func() { _ = syscall.Kill(descendantPID, syscall.SIGKILL) }()

	assert.NilError(t, stopProcess(t.Context(), cmd, wait, 5*time.Second))
	assert.NilError(t, lifetime.Close())

	assert.NilError(t, statusR.SetReadDeadline(time.Now().Add(5*time.Second)))
	_, err = reader.ReadByte()
	assert.Check(t, errors.Is(err, io.EOF), "descendant still holds process-group pipe: %v", err)
}
