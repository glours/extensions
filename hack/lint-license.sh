#!/usr/bin/env bash
# SPDX-FileCopyrightText: Copyright The Moby Authors
# SPDX-License-Identifier: Apache-2.0

set -euo pipefail

cd "$(dirname "${BASH_SOURCE[0]}")/.."

# Check tracked sources only; vendored code keeps its upstream licensing.
git ls-files -z -- '*.go' '*.sh' '*.proto' ':!:vendor/**' | (
    status=0
    while IFS= read -r -d '' path; do
        if ! sed -n '1,5p' "$path" | grep -Eq '^(//|#) SPDX-FileCopyrightText: Copyright The Moby Authors[[:space:]]*$'; then
            printf '%s: missing Moby copyright SPDX header in the first five lines\n' "$path" >&2
            status=1
        fi
        if ! sed -n '1,5p' "$path" | grep -Eq '^(//|#) SPDX-License-Identifier: Apache-2\.0[[:space:]]*$'; then
            printf '%s: missing Apache-2.0 SPDX header in the first five lines\n' "$path" >&2
            status=1
        fi
    done
    exit "$status"
)
