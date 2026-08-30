#!/bin/bash
# Advances last_backport.commit and opens (or, in dry-run mode,
# previews) the PR containing this run's backported commits. The
# pointer commit lands in the same PR as the code, so
# last_backport.commit only really advances once the PR is merged.
set -euo pipefail

: "${OUT_DIR:?OUT_DIR must be set}"
: "${WINDOW_END:?WINDOW_END must be set}"
: "${BRANCH_NAME:?BRANCH_NAME must be set}"
: "${LAST_BACKPORT_FILE:=last_backport.commit}"
: "${DRY_RUN:=false}"
: "${TEST_LOG:=}"
: "${TEST_OUTCOME:=skipped}"

report_md="$OUT_DIR/backport-report.md"
status_file="$OUT_DIR/backport-status.txt"
build_log="$OUT_DIR/build.log"
status="$( [ -f "$status_file" ] && cat "$status_file" || echo unknown)"

printf '%s\n' "$WINDOW_END" > "$LAST_BACKPORT_FILE"
git add "$LAST_BACKPORT_FILE"
git commit -q -m "chore(backport): advance last_backport.commit to ${WINDOW_END:0:8}" \
  --author="github-actions[bot] <41898282+github-actions[bot]@users.noreply.github.com>"

body_file="$OUT_DIR/pr-body.md"
{
  echo "Automated weekly sync from [charmbracelet/crush](https://github.com/charmbracelet/crush)."
  echo
  echo "- Triaged through upstream commit \`${WINDOW_END}\`."
  echo "- \`last_backport.commit\` now points at \`${WINDOW_END}\`; merging this PR is what advances it."
  echo

  if [ -s "$report_md" ]; then
    cat "$report_md"
    echo
  fi

  if [ "$status" = "failed" ]; then
    echo "## Needs human attention"
    echo
    echo "The backport was committed, but \`go build ./...\` (or \`go mod tidy\`) failed afterwards. Log tail:"
    echo
    echo '```'
    tail -n 60 "$build_log" 2>/dev/null
    echo '```'
    echo
  fi

  echo "## Verification"
  echo
  echo "- \`gofumpt\` + \`go build ./...\` ran once after the backport pass: **${status}**."
  echo "- Full suite: \`go test ./...\` -> **${TEST_OUTCOME}**."
  if [ -n "$TEST_LOG" ] && [ "$TEST_OUTCOME" != "success" ] && [ -f "$TEST_LOG" ]; then
    echo
    echo '```'
    tail -n 60 "$TEST_LOG"
    echo '```'
  fi
} > "$body_file"

title="chore(backport): upstream bugfixes through ${WINDOW_END:0:8}"

if [ "$DRY_RUN" = "true" ]; then
  {
    echo "## Backport dry run"
    echo
    echo "Branch \`$BRANCH_NAME\` was built locally but not pushed."
    echo
    echo "### PR title"
    echo
    echo "$title"
    echo
    echo "### PR body"
    echo
    cat "$body_file"
  } >> "${GITHUB_STEP_SUMMARY:-/dev/stdout}"
  exit 0
fi

git push origin "$BRANCH_NAME"

pr_flags=(--title "$title" --body-file "$body_file" --base main --head "$BRANCH_NAME")
if [ "$status" = "failed" ]; then
  pr_flags+=(--draft)
fi
gh pr create "${pr_flags[@]}"
