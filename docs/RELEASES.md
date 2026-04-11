# Helios Release Automation

This repository now supports **automated GitHub releases** with generated changelogs.

Releases are both:

- **Tag-driven** (push `v*` tag), and
- **One-command** (local helper script / Make target)

## Release paths

You can release Helios in any of these ways:

1. **One-command local release (recommended for maintainers)**
2. **Manual GitHub Action dispatch**
3. **Direct tag push (tag-driven)**

---

## 1) One-command local release

From the repository root:

```bash
make release VERSION=v0.2.0 TARGET=main
```

Prerelease examples:

```bash
# Release candidate
make release VERSION=v0.2.0 CHANNEL=rc PRERELEASE_ITERATION=1 TARGET=main

# Beta release
make release VERSION=v0.2.0 CHANNEL=beta PRERELEASE_ITERATION=1 TARGET=main
```

What this does:

- Validates version format (`vMAJOR.MINOR.PATCH`)
- Verifies clean working tree
- Creates an annotated tag on `origin/main`
- Pushes the tag
- Triggers the tag-driven release workflow

Dry run (no tag push):

```bash
make release VERSION=v0.2.0 CHANNEL=rc PRERELEASE_ITERATION=1 DRY_RUN=true
```

---

## 2) Manual GitHub Action dispatch

Use workflow: **Create Release Tag (Manual)**

Inputs:

- `version` → base or full tag (e.g. `v0.2.0` or `v0.2.0-rc.1`)
- `target` → branch to tag (default `main`)
- `channel` → `stable` | `rc` | `beta`
- `prerelease_iteration` → used when channel is `rc`/`beta` and version has no suffix

This workflow pushes the tag; release publication is then handled automatically by the tag-driven workflow.

---

## 3) Tag-driven release (direct)

Push a tag manually:

```bash
git tag -a v0.2.0 -m "Release v0.2.0"
git push origin v0.2.0
```

That triggers **Release (Tag-Driven)**.

---

## What the automated release workflow does

Workflow: `.github/workflows/release-on-tag.yml`

For each `v*` tag push, it:

1. Checks out full git history (for changelog ranges)
2. Runs tests serially (`go test -p 1 ./... -count=1`)
3. Builds artifacts with an OS matrix:
	- Linux (`linux/amd64`)
	- macOS (`darwin/amd64`)
	- Windows (`windows/amd64`)
4. Packages artifacts per platform (`.tar.gz` for Linux/macOS, `.zip` for Windows)
5. Generates per-artifact SHA-256 checksum files
6. Generates release notes via `scripts/generate_changelog.sh`
7. Publishes/updates the GitHub Release with all packaged assets
8. Automatically marks releases as **pre-release** when tag contains `-rc` or `-beta`

---

## Prerelease channel behavior

- Tags containing `-rc` or `-beta` are published as **GitHub prereleases**.
- Stable tags (e.g., `v1.2.3`) are published as normal releases.
- Manual workflow + local script both support channel-driven tag generation.

---

## Changelog generation

Script: `scripts/generate_changelog.sh`

Manual usage:

```bash
bash scripts/generate_changelog.sh v0.2.0 v0.1.0
```

Or via Makefile:

```bash
make changelog VERSION=v0.2.0 PREVIOUS=v0.1.0
```

The generated markdown is used as the GitHub Release body.

---

## Files added for release automation

- `.github/workflows/release-on-tag.yml`
- `.github/workflows/create-release-tag.yml`
- `scripts/build_release_artifacts.sh`
- `scripts/release.sh`
- `scripts/generate_changelog.sh`
- `docs/RELEASES.md`

And Makefile targets:

- `make release VERSION=vX.Y.Z [TARGET=main]`
- `make release VERSION=vX.Y.Z [TARGET=main] [CHANNEL=stable|rc|beta] [PRERELEASE_ITERATION=1] [DRY_RUN=true|false]`
- `make release-rc VERSION=vX.Y.Z [PRERELEASE_ITERATION=1]`
- `make release-beta VERSION=vX.Y.Z [PRERELEASE_ITERATION=1]`
- `make changelog VERSION=vX.Y.Z [PREVIOUS=vA.B.C]`
