#!/bin/bash
# Fetches the upstream crush repository and lists the commits between
# last_backport.commit (or SINCE_OVERRIDE) and the upstream default
# branch, capped at WINDOW_MAX. Writes discover outputs to
# $GITHUB_OUTPUT and the candidate commit list to
# $OUT_DIR/upstream-commits.txt.
#
# Two guards keep this from running away or fighting a review in
# progress: the WINDOW_MAX cap below, and skipping entirely when a
# previous backport PR is still open.
set -euo pipefail

: "${WINDOW_MAX:=40}"
: "${UPSTREAM_URL:=https://github.com/charmbracelet/crush.git}"
: "${UPSTREAM_BRANCH:=main}"
: "${LAST_BACKPORT_FILE:=last_backport.commit}"
: "${SINCE_OVERRIDE:=}"
: "${OUT_DIR:?OUT_DIR must be set}"
: "${GITHUB_OUTPUT:?GITHUB_OUTPUT must be set}"

skip() {
  echo "has_work=false" >> "$GITHUB_OUTPUT"
  echo "skip_reason=$1" >> "$GITHUB_OUTPUT"
  echo "$2"
  exit 0
}

if git remote get-url crush >/dev/null 2>&1; then
  git remote set-url crush "$UPSTREAM_URL"
else
  git remote add crush "$UPSTREAM_URL"
fi
git fetch --no-tags crush "$UPSTREAM_BRANCH"

since="${SINCE_OVERRIDE:-$(<"$LAST_BACKPORT_FILE")}"
git rev-parse --verify "${since}^{commit}" >/dev/null

mapfile -t all_shas < <(git rev-list --reverse --no-merges "${since}..crush/${UPSTREAM_BRANCH}")

if [ "${#all_shas[@]}" -eq 0 ]; then
  skip no_new_commits "No new upstream commits since ${since}."
fi

# A previous run's PR is still awaiting review: it already owns the
# next last_backport.commit advance, so starting another one here
# would race it for the same pointer update.
if command -v gh >/dev/null 2>&1; then
  open_backport_prs="$(gh pr list --state open --json headRefName --jq '.[].headRefName' 2>/dev/null \
    | grep -c '^backport/upstream-' || true)"
  if [ "${open_backport_prs:-0}" -gt 0 ]; then
    skip pr_open "A backport PR is already open; skipping until it merges or closes."
  fi
fi

window_size="${#all_shas[@]}"
if [ "$window_size" -gt "$WINDOW_MAX" ]; then
  window_size="$WINDOW_MAX"
fi

: > "$OUT_DIR/upstream-commits.txt"
for ((i = 0; i < window_size; i++)); do
  sha="${all_shas[$i]}"
  subject="$(git log -1 --format=%s "$sha")"
  printf '%s %s\n' "$sha" "$subject" >> "$OUT_DIR/upstream-commits.txt"
done

window_end="${all_shas[$((window_size - 1))]}"

echo "has_work=true" >> "$GITHUB_OUTPUT"
echo "window_end=$window_end" >> "$GITHUB_OUTPUT"
echo "count=$window_size" >> "$GITHUB_OUTPUT"
echo "Found $window_size upstream commit(s) to triage (of ${#all_shas[@]} available), ending at $window_end."
