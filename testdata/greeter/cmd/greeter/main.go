// SPDX-FileCopyrightText: Copyright The Moby Authors
// SPDX-License-Identifier: Apache-2.0

// Command greeter serves the greeter fixture as an out-of-process extension
// that opts into socket exposure, for the integration test.
package main

import (
	greeterpb "github.com/moby/extensions/example/greeter/v0/protogen"
	"github.com/moby/extensions/sdk"
	"github.com/moby/extensions/testdata/greeter"
)

func main() {
	sdk.Main(greeter.Extension, sdk.WithServerPoints(greeterpb.ServerPoint))
}
