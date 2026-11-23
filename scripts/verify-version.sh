#!/usr/bin/env bash
set -euo pipefail
binary=$1
version=$2
commit=$3
# Inspect every architecture without depending on Rosetta for cross-built binaries.
metadata=$(go version -m "$binary")
[[ "$metadata" == *"-X gatekeep/internal/buildinfo.Version=$version -X gatekeep/internal/buildinfo.Commit=$commit"* ]] || {
  echo "Binary metadata does not match expected version and commit: $binary" >&2
  exit 1
}
# Also exercise the CLI for the host architecture before packaging.
if [[ "$metadata" == *"GOARCH=$(go env GOHOSTARCH)"* ]]; then
  actual=$("$binary" --version)
  [[ "$actual" == "GateKeep $version ($commit)" ]] || {
    echo "Binary --version did not match expected build: $actual" >&2
    exit 1
  }
fi
