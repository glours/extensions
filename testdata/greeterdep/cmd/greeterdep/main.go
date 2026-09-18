// SPDX-FileCopyrightText: Copyright The Moby Authors
// SPDX-License-Identifier: Apache-2.0

// Command greeterdep serves the greeterdep fixture as an out-of-process
// extension that depends on the greeter point and calls it at init.
package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	greeterpb "github.com/moby/extensions/example/greeter/v0/protogen"
	"github.com/moby/extensions/sdk"
	"github.com/moby/extensions/testdata/greeterdep"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	srv := sdk.NewServer()
	if err := srv.Register(greeterdep.Extension); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	srv.Depends(greeterpb.ClientPoint)
	if err := srv.Listen(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
