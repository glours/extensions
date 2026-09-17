// SPDX-FileCopyrightText: Copyright The Moby Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gotest.tools/v3/assert"
)

func TestGeneratedFilesInheritContractNotices(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		header string
	}{
		{
			name: "Moby notices",
			header: "// SPDX-FileCopyrightText: Copyright The Moby Authors\n" +
				"// SPDX-License-Identifier: Apache-2.0\n\n",
		},
		{
			name: "multiple copyright holders and another license expression",
			header: "// SPDX-FileCopyrightText: Copyright Example Authors\n" +
				"// SPDX-FileCopyrightText: Copyright Other Authors\n" +
				"// SPDX-License-Identifier: MIT OR Apache-2.0\n\n",
		},
		{name: "license only", header: "// SPDX-License-Identifier: Apache-2.0\n\n"},
		{name: "copyright only", header: "// SPDX-FileCopyrightText: Copyright Example Authors\n\n"},
		{name: "no notices"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			assert.NilError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module example.com/contract\n"), 0o644))
			const contract = `package contract

// SPDX-FileCopyrightText: ignore comments after the package clause
// SPDX-License-Identifier: ignore comments after the package clause
type Runtime interface{ Do(ctx interface{}, req *Req) (*Resp, error) }
type Req struct{}
type Resp struct{}
`
			assert.NilError(t, os.WriteFile(filepath.Join(dir, "contract.go"), []byte(tc.header+contract), 0o644))
			// A different file's notices must not override the interface's notices.
			const other = `// SPDX-FileCopyrightText: Copyright Unrelated Authors
// SPDX-License-Identifier: BSD-3-Clause

package contract
`
			assert.NilError(t, os.WriteFile(filepath.Join(dir, "other.go"), []byte(other), 0o644))
			assert.NilError(t, run(dir, "example.contract.v1.Runtime"))
			for _, name := range []string{"runtime.proto", "protogen/runtime.pb.go", "protogen/wire.gen.go"} {
				content, err := os.ReadFile(filepath.Join(dir, name))
				assert.NilError(t, err)
				header, _, ok := strings.Cut(string(content), "// Code generated")
				assert.Assert(t, ok, "%s must have a generated code marker", name)
				assert.Equal(t, header, tc.header, "%s must preserve only the contract notices", name)
			}
		})
	}
}

func TestSingleMessageField(t *testing.T) {
	pt, err := parsePoint("testdata/singlemsg")
	assert.NilError(t, err)
	pt.importPath = "example.com/singlemsg"

	proto, err := emitProto(pt)
	assert.NilError(t, err)
	assert.Check(t, strings.Contains(string(proto), "Nested nested = 1;"),
		"proto should declare a single (non-repeated) message field:\n%s", proto)
	assert.Check(t, !strings.Contains(string(proto), "repeated Nested"),
		"single message field must not be repeated:\n%s", proto)

	wire, err := emitWire(pt)
	assert.NilError(t, err)
	src := string(wire)
	assert.Check(t, strings.Contains(src, "out.Nested = nestedToProto(in.Nested)"),
		"wire should convert the single message to proto by pointer:\n%s", src)
	assert.Check(t, strings.Contains(src, "out.Nested = nestedFromProto(in.GetNested())"),
		"wire should convert the single message from proto by pointer:\n%s", src)
}

func TestInitialismFieldBridgesNames(t *testing.T) {
	const src = `package p
import "github.com/moby/extensions"
type S interface{ Do(ctx interface{}, req *Req) (*Resp, error) }
type Req struct{ ContainerID string ` + "`pb:\"1\"`" + ` }
type Resp struct{ Ok bool ` + "`pb:\"1\"`" + ` }
var Point = extensions.DefinePoint[S]("test.gen.v1")
`
	pt, err := parseSource(t, src)
	assert.NilError(t, err)
	pt.importPath = "example.com/p"

	proto, err := emitProto(pt)
	assert.NilError(t, err)
	assert.Check(t, strings.Contains(string(proto), "string container_id = 1;"),
		"proto field must be clean snake_case, not container_i_d:\n%s", proto)

	wire, err := emitWire(pt)
	assert.NilError(t, err)
	src2 := string(wire)
	assert.Check(t, strings.Contains(src2, "out.ContainerId = in.ContainerID"),
		"ToProto must set proto ContainerId from contract ContainerID:\n%s", src2)
	assert.Check(t, strings.Contains(src2, "out.ContainerID = in.GetContainerId()"),
		"FromProto must set contract ContainerID from proto GetContainerId():\n%s", src2)
}

func TestServiceContractWithoutAPoint(t *testing.T) {
	const contract = `package p
type Req struct{ Name string ` + "`pb:\"1\"`" + ` }
type Resp struct{ Ok bool ` + "`pb:\"1\"`" + ` }

type Runtime interface{ Do(ctx interface{}, req *Req) (*Resp, error) }
`
	pt, err := parseServiceSource(t, contract, "my.proto.pkg.v1.Runtime")
	assert.NilError(t, err)
	assert.Equal(t, pt.iface, "Runtime")
	assert.Equal(t, pt.id, "my.proto.pkg.v1")
	assert.Equal(t, pt.service, "Runtime")
	assert.Equal(t, pt.grpcService(), "my.proto.pkg.v1.Runtime")
	assert.Check(t, !pt.isPoint, "a contract with no DefinePoint is not a point")

	pt.importPath = "example.com/p"
	wire, err := emitWire(pt)
	assert.NilError(t, err)
	src := string(wire)
	assert.Check(t, strings.Contains(src, "func RegisterServer(r grpc.ServiceRegistrar, impl p.Runtime)"), src)
	assert.Check(t, strings.Contains(src, "func NewClient(conn grpc.ClientConnInterface) p.Runtime"), src)
	assert.Check(t, !strings.Contains(src, "ServerPoint"), "a non-point contract must not emit point registrations:\n%s", src)
	assert.Check(t, !strings.Contains(src, "clientpoint"), "a non-point contract must not import the point packages:\n%s", src)
	assert.Check(t, !strings.Contains(src, "servicev0"), "an unpublished contract must not emit typed publication APIs:\n%s", src)
}

func TestOrdinaryPointGeneratesPublicationAndClientAPIs(t *testing.T) {
	const contract = `package p
import "github.com/moby/extensions"
type Req struct{ Name string ` + "`pb:\"1\"`" + ` }
type Resp struct{ Ok bool ` + "`pb:\"1\"`" + ` }

type Runtime interface {
	Do(ctx interface{}, req *Req) (*Resp, error)
	Delete(ctx interface{}, req *Req) error
}
var Point = extensions.DefinePoint[Runtime]("my.proto.pkg.v1")
`
	pt, err := parseSource(t, contract)
	assert.NilError(t, err)
	assert.Equal(t, pt.service, "Runtime")
	assert.Equal(t, pt.grpcService(), "my.proto.pkg.v1.Runtime")
	pt.importPath = "example.com/p"

	wire, err := emitWire(pt)
	assert.NilError(t, err)
	src := string(wire)
	assert.Check(t, strings.Contains(src, "var ServerPoint = serverpoint.Registration{"), src)
	assert.Check(t, strings.Contains(src, "var ClientPoint = clientpoint.Registration{"), src)
	assert.Check(t, strings.Contains(src, "func NewRuntimeClient(cc grpc.ClientConnInterface) RuntimeClient"),
		"ordinary point must retain its raw gRPC client:\n%s", src)
	assert.Check(t, strings.Contains(src, "func NewClient(conn grpc.ClientConnInterface) p.Runtime"),
		"ordinary point must expose its handwritten client:\n%s", src)
	assert.Check(t, !strings.Contains(src, "servicev0") && !strings.Contains(src, "func Bind("),
		"ordinary Point transport must not contain extension-side publication bindings:\n%s", src)
	assert.Check(t, !strings.Contains(src, "var Service") && !strings.Contains(src, "typedClient"),
		"deleted typed-definition mode must not be generated:\n%s", src)
}

func TestReservedFieldNumbers(t *testing.T) {
	const header = `package p
import "github.com/moby/extensions"
type S interface{ Do(ctx interface{}, req *Req) (*Resp, error) }
type Resp struct{ Ok bool ` + "`pb:\"1\"`" + ` }

var Point = extensions.DefinePoint[S]("test.gen.v1")
`
	t.Run("emits reserved", func(t *testing.T) {
		pt, err := parseSource(t, header+"\ntype Req struct { Name string `pb:\"1\"`; _ struct{} `pb:\"2\"`; _ struct{} `pb:\"4\"` }\n")
		assert.NilError(t, err)
		pt.importPath = "example.com/p"
		proto, err := emitProto(pt)
		assert.NilError(t, err)
		assert.Check(t, strings.Contains(string(proto), "reserved 2;\n  reserved 4;"),
			"proto should reserve both burned numbers:\n%s", proto)
	})

	t.Run("rejects a field reusing a reserved number", func(t *testing.T) {
		_, err := parseSource(t, header+"\ntype Req struct { Name string `pb:\"2\"`; _ struct{} `pb:\"2\"` }\n")
		assert.ErrorContains(t, err, "field number 2 is used by both")
	})

	t.Run("rejects a non-numeric reservation", func(t *testing.T) {
		_, err := parseSource(t, header+"\ntype Req struct { Name string `pb:\"1\"`; _ struct{} `pb:\"two\"` }\n")
		assert.ErrorContains(t, err, "not a field number")
	})

	t.Run("rejects an oversized reservation", func(t *testing.T) {
		_, err := parseSource(t, header+"\ntype Req struct { Name string `pb:\"1\"`; _ struct{} `pb:\"536870912\"` }\n")
		assert.ErrorContains(t, err, "must be <= 536870911")
	})

	t.Run("rejects a reservation with storage", func(t *testing.T) {
		_, err := parseSource(t, header+"\ntype Req struct { Name string `pb:\"1\"`; _ string `pb:\"2\"` }\n")
		assert.ErrorContains(t, err, "must use `_ struct{}`")
	})

	t.Run("descriptor accepts reservation boundaries", func(t *testing.T) {
		pt, err := parseSource(t, header+"\ntype Req struct { Name string `pb:\"1\"`; _ struct{} `pb:\"19000\"`; _ struct{} `pb:\"536870911\"` }\n")
		assert.NilError(t, err)
		fd, err := fileDescriptor(pt)
		assert.NilError(t, err)
		ranges := fd.MessageType[0].ReservedRange
		assert.Equal(t, ranges[0].GetStart(), int32(19000))
		assert.Equal(t, ranges[0].GetEnd(), int32(19001))
		assert.Equal(t, ranges[1].GetStart(), int32(536870911))
		assert.Equal(t, ranges[1].GetEnd(), int32(536870912))
	})
}

func TestSinglePointCardinality(t *testing.T) {
	const contract = `package p
import "github.com/moby/extensions"
type S interface{ Do(ctx interface{}, req *Req) (*Resp, error) }
type Req struct{ Name string ` + "`pb:\"1\"`" + ` }
type Resp struct{ Ok bool ` + "`pb:\"1\"`" + ` }
`
	t.Run("DefineSinglePoint marks the ClientPoint", func(t *testing.T) {
		pt, err := parseSource(t, contract+"var Point = extensions.DefineSinglePoint[S](\"test.gen.v1\")\n")
		assert.NilError(t, err)
		assert.Check(t, pt.isSingle)
		pt.importPath = "example.com/p"
		wire, err := emitWire(pt)
		assert.NilError(t, err)
		assert.Check(t, strings.Contains(string(wire), "Provider: ClientProvider, Single: true"),
			"the generated ClientPoint must carry the contract's cardinality:\n%s", wire)
	})

	t.Run("DefinePoint does not", func(t *testing.T) {
		pt, err := parseSource(t, contract+"var Point = extensions.DefinePoint[S](\"test.gen.v1\")\n")
		assert.NilError(t, err)
		assert.Check(t, !pt.isSingle)
		pt.importPath = "example.com/p"
		wire, err := emitWire(pt)
		assert.NilError(t, err)
		assert.Check(t, !strings.Contains(string(wire), "Single"),
			"a fan-out point must not claim single cardinality:\n%s", wire)
	})
}
