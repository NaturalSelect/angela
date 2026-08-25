#!/usr/bin/env bash
# Re-record the VCR cassettes behind TestCoderAgent.
#
# The recorder runs in ModeRecordOnce: it replays whenever a cassette
# file exists and only talks to the provider when one is missing. So
# re-recording means deleting the cassette first — this script does
# that, records, then replays what it recorded to prove the result is
# usable.
#
# THIS SPENDS REAL MONEY. Each cassette is a full multi-turn tool-using
# conversation against a live model.
#
# When you need it: the request body has to match the recording, and the
# whole system prompt is part of that body. Editing a builtin skill's
# `name` or `description` (internal/skills/builtin/*/SKILL.md) changes
# the prompt for every request and invalidates all cassettes at once.
# The failure mode is not a clear mismatch error — the test falls
# through to a live call and hangs until it times out. A skill's body is
# not in the prompt, so editing that alone needs no re-recording.
#
# Usage:
#   scripts/record.sh              # re-record every cassette
#   scripts/record.sh update_a_file  # re-record one, by subtest name
set -euo pipefail

cd "$(git rev-parse --show-toplevel)"

readonly CASSETTES="internal/agent/testdata/TestCoderAgent"
readonly PKG="./internal/agent/"
filter="${1:-}"

if [[ -z "${MUNDUS_API_KEY:-}" ]]; then
  echo "MUNDUS_API_KEY is not set; recording would produce empty cassettes." >&2
  echo "Export it and re-run. It is read by hyperBuilder in internal/agent/common_test.go." >&2
  exit 1
fi

if [[ -n "$filter" ]]; then
  # An empty middle element matches every model directory.
  run="TestCoderAgent//${filter}"
  mapfile -t doomed < <(find "$CASSETTES" -name "*${filter}*.yaml")
else
  run="TestCoderAgent"
  mapfile -t doomed < <(find "$CASSETTES" -name '*.yaml')
fi

if [[ ${#doomed[@]} -eq 0 ]]; then
  echo "No cassettes matched ${filter:-<all>} under ${CASSETTES}." >&2
  exit 1
fi

echo "About to delete and re-record ${#doomed[@]} cassette(s) against the live provider:"
printf '  %s\n' "${doomed[@]}"
read -rp "Continue? [y/N] " reply
[[ "$reply" == [yY] ]] || { echo "Aborted; nothing was deleted."; exit 1; }

rm -f -- "${doomed[@]}"

echo "==> Recording (live calls, this is the slow part)"
go test -count=1 -timeout 1800s "$PKG" -run "$run"

echo "==> Replaying to verify the recording is usable"
go test -count=1 -timeout 300s "$PKG" -run "$run"

echo
echo "Done. Review the diff before committing:"
echo "  git diff --stat ${CASSETTES}"
