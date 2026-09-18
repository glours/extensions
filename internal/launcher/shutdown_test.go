// SPDX-FileCopyrightText: Copyright The Moby Authors
// SPDX-License-Identifier: Apache-2.0

package launcher

import (
	"context"
	"errors"
	"os/exec"
	"testing"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"gotest.tools/v3/assert"
)

type recordingProcessLifetime struct {
	calls int
	err   error
}

func (l *recordingProcessLifetime) Close() error {
	l.calls++
	return l.err
}

func TestProcessShutdownCloseIsIdempotentAndRetainsError(t *testing.T) {
	conn, err := grpc.NewClient("passthrough:///unused",
		grpc.WithTransportCredentials(insecure.NewCredentials()))
	assert.NilError(t, err)
	lifetimeErr := errors.New("close lifetime")
	lifetime := &recordingProcessLifetime{err: lifetimeErr}
	shutdown := &processShutdown{
		conn:     conn,
		cmd:      &exec.Cmd{},
		wait:     make(chan error),
		lifetime: lifetime,
	}

	firstErr := shutdown.Close(context.Background())
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	secondErr := shutdown.Close(canceled)

	assert.Assert(t, errors.Is(firstErr, lifetimeErr))
	assert.Assert(t, errors.Is(secondErr, lifetimeErr))
	assert.Equal(t, secondErr, firstErr)
	assert.Equal(t, lifetime.calls, 1)
}
