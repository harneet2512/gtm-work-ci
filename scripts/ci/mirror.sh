#!/usr/bin/env bash
# Push a code-only snapshot of a private branch to the public CI mirror, so GitHub Actions runs there for free.
#
#   scripts/ci/mirror.sh <branch-or-commit> [mirror-branch]
#
# The snapshot excludes every Markdown file, docs/ and verbatim ticket text (*har97*lines.txt): no
# document goes public. Each mirror branch is a fresh orphan snapshot per push, so no private history is
# ever published. CI results live at https://github.com/harneet2512/gtm-work-ci/actions.
# Tests that read the stripped documents are skipped in the mirror (scripts/ci/mirror_skips.json); they
# must still pass locally before a merge.
set -euo pipefail

REF="${1:?usage: mirror.sh <branch-or-commit> [mirror-branch]}"
REPO_ROOT="$(git rev-parse --show-toplevel)"
SHA="$(git -C "$REPO_ROOT" rev-parse --verify "$REF^{commit}")"
MBRANCH="${2:-$(echo "$REF" | sed 's#^origin/##')}"
MIRROR_URL="${GHOST_CI_MIRROR_URL:-https://github.com/harneet2512/gtm-work-ci.git}"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

git -C "$REPO_ROOT" archive "$SHA" | tar -x -C "$WORK"
find "$WORK" -type f \( -iname '*.md' -o -iname '*.mdx' -o -iname '*.markdown' -o -iname '*har97*lines*.txt' \) -delete
rm -rf "$WORK/docs" "$WORK/.githooks"
python "$REPO_ROOT/scripts/ci/mirror_workflow.py" "$WORK/.github/workflows/ci.yml" "$REPO_ROOT/scripts/ci/mirror_skips.json"

leaked="$(find "$WORK" -type f \( -iname '*.md' -o -name '.env' -o -iname '*cloud_access*' \) | head -1)"
if [ -n "$leaked" ]; then echo "refusing to push: $leaked survived the strip" >&2; exit 1; fi

cd "$WORK"
git init -q -b "$MBRANCH"
git add -A
git -c user.name="ci-mirror" -c user.email="ci-mirror@users.noreply.github.com" \
  commit -q -m "snapshot ${SHA:0:12} of ${REF}"
git push -q -f "$MIRROR_URL" "HEAD:refs/heads/$MBRANCH"
echo "mirrored ${SHA:0:12} -> gtm-work-ci:$MBRANCH"
echo "runs: gh run list -R harneet2512/gtm-work-ci --branch $MBRANCH"
