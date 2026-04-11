#!/usr/bin/env bash
set -euo pipefail

CURRENT_TAG="${1:-}"
PREVIOUS_TAG="${2:-}"

if [[ -z "$CURRENT_TAG" ]]; then
  echo "Usage: $0 <current-tag> [previous-tag]" >&2
  exit 1
fi

if [[ -n "$PREVIOUS_TAG" ]]; then
  RANGE="${PREVIOUS_TAG}..${CURRENT_TAG}"
else
  RANGE="$CURRENT_TAG"
fi

if ! git rev-parse "$CURRENT_TAG" >/dev/null 2>&1; then
  echo "error: tag or ref '$CURRENT_TAG' does not exist locally" >&2
  exit 1
fi

TMP_DIR="$(mktemp -d)"
cleanup() {
  rm -rf "$TMP_DIR"
}
trap cleanup EXIT

section_for_type() {
  case "$1" in
    feat) echo "features" ;;
    fix) echo "fixes" ;;
    perf) echo "performance" ;;
    refactor) echo "refactors" ;;
    docs) echo "documentation" ;;
    test) echo "tests" ;;
    build) echo "build" ;;
    ci) echo "ci" ;;
    chore) echo "chores" ;;
    revert) echo "reverts" ;;
    *) echo "other" ;;
  esac
}

while IFS=$'\t' read -r short_hash subject; do
  [[ -z "$short_hash" ]] && continue

  normalized_subject="$subject"
  commit_type="other"

  if [[ "$subject" == *:* ]]; then
    prefix="${subject%%:*}"
    suffix="${subject#*:}"
    suffix="${suffix# }"

    candidate="${prefix%%(*}"
    candidate="${candidate%%!*}"
    candidate="${candidate,,}"

    if [[ "$candidate" =~ ^[a-z]+$ ]] && [[ -n "$suffix" ]]; then
      commit_type="$candidate"
      normalized_subject="$suffix"
    fi
  fi

  section="$(section_for_type "$commit_type")"
  echo "- ${normalized_subject} (${short_hash})" >>"${TMP_DIR}/${section}.txt"
done < <(git log --pretty=format:'%h%x09%s' "$RANGE")

echo "## Changelog"
echo
if [[ -n "$PREVIOUS_TAG" ]]; then
  echo "Changes since **${PREVIOUS_TAG}**:"
  echo
else
  echo "Initial release changes:"
  echo
fi

print_section() {
  local file="$1"
  local title="$2"
  if [[ -f "$file" ]]; then
    echo "### ${title}"
    cat "$file"
    echo
  fi
}

print_section "${TMP_DIR}/features.txt" "Features"
print_section "${TMP_DIR}/fixes.txt" "Fixes"
print_section "${TMP_DIR}/performance.txt" "Performance"
print_section "${TMP_DIR}/refactors.txt" "Refactors"
print_section "${TMP_DIR}/documentation.txt" "Documentation"
print_section "${TMP_DIR}/tests.txt" "Tests"
print_section "${TMP_DIR}/build.txt" "Build"
print_section "${TMP_DIR}/ci.txt" "CI"
print_section "${TMP_DIR}/chores.txt" "Chores"
print_section "${TMP_DIR}/reverts.txt" "Reverts"
print_section "${TMP_DIR}/other.txt" "Other Changes"

if [[ -n "$PREVIOUS_TAG" && -n "${GITHUB_REPOSITORY:-}" ]]; then
  echo "**Full Changelog**: https://github.com/${GITHUB_REPOSITORY}/compare/${PREVIOUS_TAG}...${CURRENT_TAG}"
fi
