#!/usr/bin/env bash
set -euo pipefail

case "${1:-dev}" in
  dev)
    commit=$(git rev-parse --short=12 HEAD 2>/dev/null || true)
    version=dev
    if [[ -n "$commit" ]]; then version="dev-$commit"; fi
    if [[ -n "$(git status --porcelain --untracked-files=normal 2>/dev/null)" ]]; then
      version="$version-dirty"
    fi
    printf '%s\n' "$version"
    ;;
  release)
    version=${2:-}
    if [[ ! "$version" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]]; then
      echo 'Release version must be an exact vMAJOR.MINOR.PATCH tag (no leading zeroes).' >&2
      exit 1
    fi
    head=$(git rev-parse HEAD)
    tagged=$(git rev-parse --verify "refs/tags/$version^{commit}")
    if [[ "$head" != "$tagged" ]]; then
      echo "Release tag $version does not point to HEAD." >&2
      exit 1
    fi
    if [[ -n "$(git status --porcelain --untracked-files=normal)" ]]; then
      echo 'Release builds require a clean working tree, including untracked files.' >&2
      exit 1
    fi
    printf '%s\n' "$version"
    ;;
  *) echo 'Usage: version.sh dev | release vMAJOR.MINOR.PATCH' >&2; exit 1 ;;
esac
