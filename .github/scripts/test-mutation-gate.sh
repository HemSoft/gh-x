#!/usr/bin/env bash
set -euo pipefail

repo_root=$(git rev-parse --show-toplevel)
temp_dir=$(mktemp -d)
trap 'rm -rf "$temp_dir"' EXIT
fake="$temp_dir/gremlins"
cat > "$fake" <<'FAKE'
#!/usr/bin/env bash
set -euo pipefail
args=" $* "
[[ "$args" == *" --threshold-efficacy 90 "* ]]
[[ "$args" == *" --threshold-mcover 90 "* ]]
[[ "$args" == *" ./src "* ]]
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
expect_failure "$uncovered" "Mutator coverage 89.11% is below 90%"

live="$temp_dir/live.txt"
write_report "$live" 9 1 0 90.00 100.00
# Gremlins v0.6.0 treats equality as a threshold failure, so the deterministic
# live-mutant fixture preserves that tool-level nonzero result.
expect_failure "$live" "Gremlins failed with exit code 10" 10

passing="$temp_dir/passing.txt"
write_report "$passing" 91 0 9 100.00 91.00
GREMLINS_BIN="$fake" GREMLINS_FIXTURE="$passing" bash "$repo_root/.github/scripts/run-mutation-gate.sh" >/dev/null

echo "Mutation threshold fixtures passed"
