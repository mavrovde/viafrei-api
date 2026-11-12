#!/usr/bin/env bash
# docs-a11y-runner.test.sh [LCOV]
#
# The unit test of scripts/a11y-tools/axe-runner.js, the pa11y runner that
# scripts/docs-a11y.sh uses: scripts/a11y-tools/axe-runner.test.js under
# node:test, against a fake window, held to 100 % line, branch and function
# coverage (a lower figure fails). LCOV (default coverage-a11y-runner.lcov)
# receives the coverage report, which SonarCloud reads.
#
# The pinned toolchain is installed first (npm ci --ignore-scripts), because
# the runner resolves axe-core from it.
set -euo pipefail

here=$(cd "$(dirname "$0")" && pwd)
root=$(cd "$here/.." && pwd)
lcov=${1:-coverage-a11y-runner.lcov}
tools=$here/a11y-tools

npm ci --prefix "$tools" --ignore-scripts --no-audit --no-fund --loglevel=error >/dev/null ||
  { echo "FAIL: could not install the pinned toolchain in $tools" >&2; exit 2; }
cd "$root"
node --test --experimental-test-coverage \
  --test-coverage-include=scripts/a11y-tools/axe-runner.js \
  --test-coverage-lines=100 --test-coverage-branches=100 --test-coverage-functions=100 \
  --test-reporter=spec --test-reporter-destination=stdout \
  --test-reporter=lcov --test-reporter-destination="$lcov" \
  scripts/a11y-tools/axe-runner.test.js
[[ -s $lcov ]] || { echo "FAIL: no coverage report was written to $lcov" >&2; exit 2; }
grep -q '^SF:.*scripts/a11y-tools/axe-runner\.js$' "$lcov" ||
  { echo "FAIL: $lcov does not cover scripts/a11y-tools/axe-runner.js" >&2; exit 2; }
echo "docs a11y runner: unit test passed at 100 % coverage; report in $lcov"
