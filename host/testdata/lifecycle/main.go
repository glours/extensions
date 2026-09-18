// SPDX-FileCopyrightText: Copyright The Moby Authors
// SPDX-License-Identifier: Apache-2.0

// Command lifecycle is an out-of-process extension fixture used by host
// resource-ownership tests.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/signal"
	"syscall"

	"github.com/moby/extensions"
	echov1 "github.com/moby/extensions/internal/launcher/echo/v1"
	echopb "github.com/moby/extensions/internal/launcher/echo/v1/protogen"
	"github.com/moby/extensions/sdk"
)

const extensionID = extensions.ExtensionID("org.example.lifecycle.v1")

type echo struct{}

func (echo) Echo(_ context.Context, req *echov1.EchoRequest) (*echov1.EchoResponse, error) {
	return &echov1.EchoResponse{Message: req.Message}, nil
}

func run() error {
	startupJSON, err := io.ReadAll(os.Stdin)
	if err != nil {
		return fmt.Errorf("read startup config: %w", err)
	}
	var startup sdk.StartupConfig
	if err := json.Unmarshal(startupJSON, &startup); err != nil {
		return fmt.Errorf("decode startup config: %w", err)
	}
	probeFile, _ := startup.Config["probeFile"].(string)
	if probeFile == "" {
		return errors.New("probe file is required")
	}
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("listen on process probe: %w", err)
	}
	defer probe.Close()
	if err := os.WriteFile(probeFile, []byte(probe.Addr().String()), 0o644); err != nil {
		return fmt.Errorf("write process probe: %w", err)
	}

	ext := extensions.New(extensions.Declaration{
		ID:        extensionID,
		Providers: []extensions.Provider{echov1.Point.Provide(echo{})},
		Init: func(_ context.Context, cfg extensions.Config, _ extensions.Resolver) error {
			if fail, _ := cfg["failInit"].(bool); fail {
				return errors.New("requested initialization failure")
			}
			return nil
		},
	})
	srv := sdk.NewServer()
	if err := srv.Register(ext, echopb.ServerPoint); err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return srv.ListenWithIO(ctx, bytes.NewReader(startupJSON), os.Stdout)
}

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
