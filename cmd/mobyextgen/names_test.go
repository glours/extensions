// SPDX-FileCopyrightText: Copyright The Moby Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"testing"

	"gotest.tools/v3/assert"
)

func TestCamelToSnake(t *testing.T) {
	for in, want := range map[string]string{
		"ContainerID":  "container_id",
		"Name":         "name",
		"Image":        "image",
		"HTTPServer":   "http_server",
		"APIKey":       "api_key",
		"URL":          "url",
		"AddEnv":       "add_env",
		"CapAdd":       "cap_add",
		"OCISpec":      "oci_spec",
		"ContainerIDs": "container_ids",
		"CPUs":         "cpus",
		"IDs":          "ids",
		"URLsAndIDs":   "urls_and_ids",
	} {
		assert.Equal(t, camelToSnake(in), want, "camelToSnake(%q)", in)
	}
}

func TestGoCamelCaseMatchesProtoc(t *testing.T) {
	for in, want := range map[string]string{
		"container_id": "ContainerId",
		"name":         "Name",
		"http_server":  "HttpServer",
		"api_key":      "ApiKey",
		"url":          "Url",
		"add_env":      "AddEnv",
	} {
		assert.Equal(t, goCamelCase(in), want, "goCamelCase(%q)", in)
	}
}

func TestProtoFileNameFollowsService(t *testing.T) {
	for service, want := range map[string]string{
		"CreateSpecHook": "create_spec_hook",
		"Greeter":        "greeter",
		"Echo":           "echo",
	} {
		assert.Equal(t, camelToSnake(service), want, "proto file name for service %q", service)
	}
}
