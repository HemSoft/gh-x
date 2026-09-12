#!/usr/bin/env bash
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
# shellcheck source=/dev/null
source "$repo_root/.github/quality-tools.env"

: "${MUTATION_EFFICACY_THRESHOLD:?missing mutation efficacy threshold}"
: "${MUTATION_COVERAGE_THRESHOLD:?missing mutator coverage threshold}"
: "${MUTATION_PACKAGE_SCOPE:?missing mutation package scope}"

gremlins_bin=${GREMLINS_BIN:-gremlins}
cd "$repo_root"
package_scope=${1:-$MUTATION_PACKAGE_SCOPE}
results=$(mktemp)
trap 'rm -f "$results"' EXIT

set +e
"$gremlins_bin" unleash \
  --timeout-coefficient 10 \
  --threshold-efficacy "$MUTATION_EFFICACY_THRESHOLD" \
  --threshold-mcover "$MUTATION_COVERAGE_THRESHOLD" \
  "$package_scope" 2>&1 | tee "$results"
gremlins_status=${PIPESTATUS[0]}
set -e

if grep -Fq "No results to report" "$results"; then
  echo "::error::Mutation testing produced no results"
  exit 1
fi

summary=$(grep -E '^Killed: [0-9]+, Lived: [0-9]+, Not covered: [0-9]+$' "$results" | tail -1 || true)
efficacy=$(sed -n 's/^Test efficacy: \([0-9][0-9.]*\)%$/\1/p' "$results" | tail -1)
mutator_coverage=$(sed -n 's/^Mutator coverage: \([0-9][0-9.]*\)%$/\1/p' "$results" | tail -1)
if [[ -z "$summary" || -z "$efficacy" || -z "$mutator_coverage" ]]; then
  echo "::error::Mutation output did not contain killed, lived, not-covered, efficacy, and mutator-coverage metrics"
  exit 1
fi

failed=false
if awk -v value="$efficacy" -v floor="$MUTATION_EFFICACY_THRESHOLD" 'BEGIN { exit !(value < floor) }'; then
  echo "::error::Mutation efficacy ${efficacy}% is below ${MUTATION_EFFICACY_THRESHOLD}%"
  failed=true
fi
if awk -v value="$mutator_coverage" -v floor="$MUTATION_COVERAGE_THRESHOLD" 'BEGIN { exit !(value < floor) }'; then
  echo "::error::Mutator coverage ${mutator_coverage}% is below ${MUTATION_COVERAGE_THRESHOLD}%"
  failed=true
fi
if [[ "$failed" == true ]]; then
  exit 1
fi
# Gremlins v0.6.0 returns threshold codes when a value equals its floor. The
# parsed policy above treats equality as passing and still rejects lower values.
if (( gremlins_status != 0 && gremlins_status != 10 && gremlins_status != 11 )); then
  echo "::error::Gremlins failed with exit code ${gremlins_status}"
  exit "$gremlins_status"
fi
