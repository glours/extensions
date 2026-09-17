#!/usr/bin/env bash
# SPDX-FileCopyrightText: Copyright The Moby Authors
# SPDX-License-Identifier: Apache-2.0

set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

# Copy tracked working-tree files to include local edits without scratch files.
# Generate in a copy so lint also works with a read-only checkout.
workdir="$(mktemp -d)"
trap 'rm -rf "$workdir"' EXIT
mkdir "$workdir/original" "$workdir/generated"
git ls-files -z |
    tar --null -T - -cf - | tar -xf - -C "$workdir/original"
cp -a "$workdir/original/." "$workdir/generated/"
(
    cd "$workdir/generated"
    "${GO:-go}" generate ./...
)

if ! diff -ru "$workdir/original" "$workdir/generated"; then
    echo 'generated files are out of date; run go generate ./...' >&2
    exit 1
fi
