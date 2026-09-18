// SPDX-FileCopyrightText: Copyright The Moby Authors
// SPDX-License-Identifier: Apache-2.0

package launcher

import (
	"os"
	"os/exec"
	"testing"
	"time"

	"gotest.tools/v3/assert"
)

func TestProcessLifetimeKillsProcessWhenClosed(t *testing.T) {
	if os.Getenv("MOBY_TEST_PROCESS_LIFETIME_HELPER") == "1" {
		time.Sleep(time.Hour)
		return
	}

	cmd := exec.Command(os.Args[0], "-test.run=TestProcessLifetimeKillsProcessWhenClosed")
	cmd.Env = append(os.Environ(), "MOBY_TEST_PROCESS_LIFETIME_HELPER=1")
	lifetime, wait, err := startProcess(cmd)
	assert.NilError(t, err)
	t.Cleanup(func() { _ = cmd.Process.Kill() })

	assert.NilError(t, lifetime.Close())
	select {
	case <-wait:
	case <-time.After(10 * time.Second):
		t.Fatal("extension process survived closing its lifetime")
	}
}
