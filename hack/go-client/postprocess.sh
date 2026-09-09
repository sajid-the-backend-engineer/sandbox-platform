#!/usr/bin/env bash
set -euo pipefail

# Adds dynamic version (go:embed) and custom UserAgent to generated Go API clients.
# Usage: postprocess.sh <project-root> <package-name> <client-name>

if [ $# -lt 3 ]; then
  echo "Usage: $0 <project-root> <package-name> <client-name>" >&2
  exit 1
fi

PROJECT_ROOT="$1"
PACKAGE_NAME="$2"
CLIENT_NAME="$3"

cat > "$PROJECT_ROOT/version.go" << EOF
package ${PACKAGE_NAME}

import (
	_ "embed"
	"strings"
)

//go:embed VERSION
var _clientVersion string

var ClientVersion = strings.TrimSpace(_clientVersion)
EOF

grep -q 'UserAgent:.*"[^"]*"' "$PROJECT_ROOT/configuration.go" || { echo "ERROR: UserAgent string not found in configuration.go" >&2; exit 1; }
sed -i "s|UserAgent: *\"[^\"]*\"|UserAgent:        \"${CLIENT_NAME}/\" + ClientVersion|" "$PROJECT_ROOT/configuration.go"

# The generator does not emit gofmt-clean Go.
#
# Its output has misaligned struct tags and things like `toSerialize,err :=`, and the
# sed above rewrites a line without regard for alignment. None of that is caught at
# generation time, so it lands in the repository and surfaces later as a CI failure in
# a job nobody associates with regenerating a client -- 300-odd files across the two Go
# clients had drifted this way.
#
# Formatting here means the committed output is already what `go fmt ./...` would
# produce, so the check that runs in CI has nothing left to find.
gofmt -w "$PROJECT_ROOT"

echo "Postprocessed and formatted Go client at $PROJECT_ROOT"
