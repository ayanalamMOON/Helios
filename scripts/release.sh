#!/usr/bin/env bash
set -euo pipefail

RAW_VERSION="${1:-${VERSION:-}}"
TARGET="${2:-${TARGET:-main}}"
CHANNEL="${3:-${CHANNEL:-stable}}"
PRERELEASE_ITERATION="${4:-${PRERELEASE_ITERATION:-1}}"
DRY_RUN="${DRY_RUN:-false}"

if [[ -z "$RAW_VERSION" ]]; then
  echo "Usage: $0 <version-tag> [target-branch] [channel] [prerelease-iteration]" >&2
  echo "Example (stable): $0 v0.2.0 main stable" >&2
  echo "Example (rc): $0 v0.2.0 main rc 1" >&2
  exit 1
fi

case "$CHANNEL" in
  stable|rc|beta) ;;
  *)
    echo "error: channel must be one of stable, rc, beta" >&2
    exit 1
    ;;
esac

VERSION="$RAW_VERSION"
if [[ "$CHANNEL" != "stable" ]]; then
  if [[ "$VERSION" != *"-rc"* && "$VERSION" != *"-beta"* ]]; then
    VERSION="${VERSION}-${CHANNEL}.${PRERELEASE_ITERATION}"
  fi
fi

if [[ ! "$VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+([.-][0-9A-Za-z.-]+)?$ ]]; then
  echo "error: version must look like vMAJOR.MINOR.PATCH (optionally with pre-release/build suffix); got '$VERSION'" >&2
  exit 1
fi

if ! git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  echo "error: not inside a git repository" >&2
  exit 1
fi

if [[ "$DRY_RUN" != "true" && -n "$(git status --porcelain)" ]]; then
  echo "error: working tree is not clean; commit/stash changes before releasing" >&2
  exit 1
fi

if git rev-parse "$VERSION" >/dev/null 2>&1; then
  echo "error: local tag '$VERSION' already exists" >&2
  exit 1
fi

if git ls-remote --tags origin "refs/tags/$VERSION" | grep -q .; then
  echo "error: remote tag '$VERSION' already exists" >&2
  exit 1
fi

git fetch origin "$TARGET" --quiet
TARGET_REF="origin/${TARGET}"
if ! git rev-parse "$TARGET_REF" >/dev/null 2>&1; then
  echo "error: target branch '$TARGET' not found on origin" >&2
  exit 1
fi

if [[ "$DRY_RUN" == "true" ]]; then
  echo "[DRY RUN] Would create and push tag: $VERSION -> $TARGET_REF"
  echo "[DRY RUN] Channel: $CHANNEL"
  exit 0
fi

git tag -a "$VERSION" "$TARGET_REF" -m "Release $VERSION"
git push origin "$VERSION"

echo "Created and pushed tag: $VERSION -> $TARGET_REF"
echo "Tag-triggered GitHub release workflow will publish the release automatically."
