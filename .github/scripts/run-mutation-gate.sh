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
if [[ ! "$summary" =~ ^Killed:\ ([0-9]+),\ Lived:\ ([0-9]+),\ Not\ covered:\ ([0-9]+)$ || -z "$efficacy" || -z "$mutator_coverage" ]]; then
  echo "::error::Mutation output did not contain killed, lived, not-covered, efficacy, and mutator-coverage metrics"
  exit 1
fi
killed=${BASH_REMATCH[1]}
lived=${BASH_REMATCH[2]}
not_covered=${BASH_REMATCH[3]}
covered=$((killed + lived))
total=$((covered + not_covered))
efficacy_left=$((killed * 100))
efficacy_right=$((MUTATION_EFFICACY_THRESHOLD * covered))
coverage_left=$((covered * 100))
coverage_right=$((MUTATION_COVERAGE_THRESHOLD * total))

failed=false
if (( efficacy_left < efficacy_right )); then
  echo "::error::Mutation efficacy from ${killed} killed and ${lived} lived mutants is below ${MUTATION_EFFICACY_THRESHOLD}% (reported ${efficacy}%)"
  failed=true
fi
if (( coverage_left < coverage_right )); then
  echo "::error::Mutator coverage from ${covered} covered and ${not_covered} not-covered mutants is below ${MUTATION_COVERAGE_THRESHOLD}% (reported ${mutator_coverage}%)"
  failed=true
fi
if [[ "$failed" == true ]]; then
  exit 1
fi

# Gremlins v0.6.0 reserves 10 and 11 for its <= threshold checks. Suppress one
# only when the corresponding integer ratio exactly equals the configured floor.
threshold_equality=false
if (( gremlins_status == 10 && efficacy_left == efficacy_right )); then
  threshold_equality=true
elif (( gremlins_status == 11 && coverage_left == coverage_right )); then
  threshold_equality=true
fi
if (( gremlins_status != 0 )) && [[ "$threshold_equality" != true ]]; then
  echo "::error::Gremlins failed with exit code ${gremlins_status}"
  exit "$gremlins_status"
fi
