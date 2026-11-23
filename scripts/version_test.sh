#!/usr/bin/env bash
set -euo pipefail
script_dir=$(cd "$(dirname "$0")" && pwd)
fixture=$(mktemp -d)
trap 'rm -rf "$fixture"' EXIT
cd "$fixture"
git init -q
git config user.name 'Version Test'
git config user.email 'version-test@example.invalid'
echo initial > tracked
git add tracked
git commit -qm initial
git tag v1.2.3
[[ "$(bash "$script_dir/version.sh" dev)" == dev-* ]]
[[ "$(bash "$script_dir/version.sh" release v1.2.3)" == v1.2.3 ]]
reject() {
  if bash "$script_dir/version.sh" release "${1:-}" >/dev/null 2>&1; then
    echo "Unexpectedly accepted release: ${1:-missing}" >&2
    exit 1
  fi
}
reject
reject 1.2.3
reject v01.2.3
reject v1.2.3-rc.1
reject v9.9.9
echo changed >> tracked
[[ "$(bash "$script_dir/version.sh" dev)" == dev-*-dirty ]]
reject v1.2.3
git checkout -- tracked
echo untracked > untracked
reject v1.2.3
rm untracked
echo next >> tracked
git commit -qam next
reject v1.2.3
[[ "$(bash "$script_dir/version.sh" dev)" == dev-* ]]
echo 'Version validation checks passed.'
