package launcher

import (
	"bufio"
	"errors"
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"gotest.tools/v3/assert"
)

func TestPdeathsigKillsDirectChildWhenParentExits(t *testing.T) {
	statusR, statusW, err := os.Pipe()
	assert.NilError(t, err)
	defer func() { assert.NilError(t, statusR.Close()) }()

	cmd := exec.Command(os.Args[0])
	cmd.Env = launcherHelperEnv("pdeathsig-parent")
	cmd.ExtraFiles = []*os.File{statusW}
	assert.NilError(t, cmd.Start())
	_ = statusW.Close()

	reader := bufio.NewReader(statusR)
	assert.NilError(t, statusR.SetReadDeadline(time.Now().Add(10*time.Second)))
	line, err := reader.ReadString('\n')
	assert.NilError(t, err)
	childPID, err := strconv.Atoi(strings.TrimSpace(line))
	assert.NilError(t, err, line)
	defer func() { _ = syscall.Kill(childPID, syscall.SIGKILL) }()

	assert.NilError(t, cmd.Wait())
	assert.NilError(t, statusR.SetReadDeadline(time.Now().Add(5*time.Second)))
	_, err = reader.ReadByte()
	assert.Check(t, errors.Is(err, io.EOF), "direct child survived launcher-parent exit: %v", err)
}
