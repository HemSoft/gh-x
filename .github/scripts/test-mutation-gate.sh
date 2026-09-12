#!/usr/bin/env bash
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
temp_dir=$(mktemp -d)
trap 'rm -rf "$temp_dir"' EXIT
fake="$temp_dir/gremlins"
cat > "$fake" <<'FAKE'
#!/usr/bin/env bash
set -euo pipefail
expected=(unleash --timeout-coefficient 10 --threshold-efficacy 90 --threshold-mcover 90 ./src)
actual=("$@")
[[ ${#actual[@]} -eq ${#expected[@]} ]]
for index in "${!expected[@]}"; do
  [[ "${actual[index]}" == "${expected[index]}" ]]
done
cat "$GREMLINS_FIXTURE"
exit "${GREMLINS_FIXTURE_EXIT:-0}"
FAKE
chmod +x "$fake"

write_report() {
  local path=$1 killed=$2 lived=$3 uncovered=$4 efficacy=$5 coverage=$6
  cat > "$path" <<REPORT
Mutation testing completed in 1 millisecond
Killed: $killed, Lived: $lived, Not covered: $uncovered
Timed out: 0, Not viable: 0, Skipped: 0
Test efficacy: $efficacy%
Mutator coverage: $coverage%
REPORT
}

expect_failure() {
  local fixture=$1 expected=$2 fixture_exit=${3:-0} output
  if output=$(GREMLINS_BIN="$fake" GREMLINS_FIXTURE="$fixture" GREMLINS_FIXTURE_EXIT="$fixture_exit" bash "$repo_root/.github/scripts/run-mutation-gate.sh" 2>&1); then
    echo "expected mutation gate failure for $fixture" >&2
    exit 1
  fi
  if ! grep -Fq "$expected" <<< "$output"; then
    printf 'missing diagnostic %q in:\n%s\n' "$expected" "$output" >&2
    exit 1
  fi
}

uncovered="$temp_dir/uncovered.txt"
write_report "$uncovered" 90 0 11 100.00 89.11
expect_failure "$uncovered" "Mutator coverage from 90 covered and 11 not-covered mutants is below 90%"

live="$temp_dir/live.txt"
write_report "$live" 8 1 0 88.89 100.00
expect_failure "$live" "Mutation efficacy from 8 killed and 1 lived mutants is below 90%" 10

rounded="$temp_dir/rounded.txt"
write_report "$rounded" 1808 0 201 100.00 90.00
expect_failure "$rounded" "Mutator coverage from 1808 covered and 201 not-covered mutants is below 90%" 11

passing="$temp_dir/passing.txt"
write_report "$passing" 90 0 10 100.00 90.00
# Equality satisfies the documented floor even though Gremlins v0.6.0 returns
# its threshold code for an exact match.
GREMLINS_BIN="$fake" GREMLINS_FIXTURE="$passing" GREMLINS_FIXTURE_EXIT=11 bash "$repo_root/.github/scripts/run-mutation-gate.sh" >/dev/null

echo "Mutation threshold fixtures passed"
