# Releasing myworktree

Runbook for cutting a new release. Follow it top to bottom; every step matches
how `v0.4.2` (PR #61) and `v0.4.3` (PR #77) were shipped.

## Model

- **`develop`** = integration branch. All feature/fix PRs land here.
- **`main`** = release branch. Only release PRs (`chore(release): vX.Y.Z`) land
  here, always **squash-merged** (linear history, no merge commits).
- **Tags** = annotated tags on `main`, named `vX.Y.Z`. Pushing a `v*` tag runs
  `.github/workflows/release.yml`, which builds the platform archives and
  publishes the GitHub Release. There is no version constant in the source —
  the workflow injects `Version`/`Commit`/`BuildDate` via `-ldflags -X`, so
  **no code change is needed to bump the version**.

## 1. Preflight

```bash
git checkout develop && git pull --ff-only origin develop
git fetch origin main
git status --porcelain            # must be empty
git log --oneline origin/main..develop   # the release delta — sanity-check it
```

Run the local CI gates (same as `.github/workflows/go-ci.yml`):

```bash
test -z "$(gofmt -l .)"
go test ./...
go build -o myworktree ./cmd/myworktree
go build -o mw ./cmd/mw
```

All four must pass before continuing.

## 2. Release-prep branch

```bash
git checkout -b chore/vX.Y.Z-release-prep develop
```

One commit, `chore(release): prepare vX.Y.Z`, touching **only**:

| File | Change |
|---|---|
| `CHANGELOG.md` | Rename `## Unreleased` → `## vX.Y.Z (YYYY-MM-DD)` with a one-line release summary under it; keep an empty `## Unreleased` placeholder at the top. **Audit the delta for PRs that shipped without a CHANGELOG entry** (`git log --oneline <prev-tag>..develop` / merged PR list) and add the missing entries in house style (bold title — em-dash — detail — `Pinned by TestName`). |
| `README.md` | Bump every `vA.B.C` string: download URLs, example tarball names, `-v` pin example, "current recommended public release" wording. |
| `README.zh-CN.md` | Same sweep as `README.md`. |
| `scripts/install.sh` | Bump the `-v` example version strings in the header/usage comments. |

No code changes in this commit. Commit-message template (see `a69ba7e` /
`ccd6d95` for worked examples): sections `CHANGELOG:` / `README:` /
`install.sh:` describing the sweep, footer `No code changes; release prep only.`

## 3. Release PR to `main`

```bash
git push -u origin chore/vX.Y.Z-release-prep
gh pr create --base main --head chore/vX.Y.Z-release-prep \
  --title "chore(release): vX.Y.Z" --body "<content delta + prep summary>"
```

- PR body lists the delta (`main..develop`: merged PRs, fixed issues).
- Wait for **all four checks** (Go CI `test-build` × {ubuntu, macOS}, Reasonix
  contract `verify` × {ubuntu, macOS}).
- **Squash merge**, delete the branch:

```bash
gh pr merge <N> --squash --delete-branch
```

> Flaky-test note: `TestRestartCreatesNewID` (`internal/framework`) has a known
> TempDir-cleanup race on ubuntu. If that is the only failure, re-run the
> failed job (`gh run rerun <run-id> --failed`) before investigating anything
> else.

## 4. Tag and publish

```bash
git fetch origin main
git tag -a vX.Y.Z -m "Release vX.Y.Z" origin/main
git push origin vX.Y.Z
```

Tag rules:

- **Annotated** tag (`-a`), message `Release vX.Y.Z` — matches `v0.4.2`+.
- Tag the squash-merge commit on `main` (its tip), never a prep-branch commit.
- Tag numbers are immutable: a skipped/superseded number stays skipped
  (`v0.4.1` was never tagged — see `a69ba7e`). Never re-tag or move a pushed
  tag; if a release is bad, cut the next patch version (the withdrawn `v0.1.0`
  assets are the cautionary tale).

## 5. Verify the release

The `Release` workflow runs three jobs: `build-platforms` (darwin/linux ×
amd64/arm64), `codesign-notarize` (**skipped** unless the repo variable
`APPLE_ENABLE_CODESIGN=true`), `publish` (auto-generated notes +
`checksums.txt`). Watch it and check the assets:

```bash
gh run list --workflow release.yml --limit 1
gh run watch <run-id> --exit-status
gh release view vX.Y.Z --json assets --jq '[.assets[].name]'
# expect: 4 tarballs + checksums.txt
curl -fsSI https://github.com/linletian/myworktree/releases/latest | grep -i '^location:'
# expect: .../releases/tag/vX.Y.Z
```

The last check matters because `scripts/install.sh` (no `-v`) resolves the
version dynamically via the `/releases/latest` redirect — it is what the
one-line install serves to users.

## 6. Back-merge into `develop`

```bash
git checkout develop && git pull --ff-only origin develop
git merge origin/main -m "Merge main (vX.Y.Z release) into develop"
git push origin develop
```

Expected conflicts: **only `CHANGELOG.md`**, and every hunk should be
"develop side empty, main side has the new section/entries" — resolve by
taking `origin/main`'s side, then confirm `git diff origin/main -- CHANGELOG.md
README.md README.zh-CN.md scripts/install.sh` is empty before committing.
Anything beyond that shape means the prep commit missed something — stop and
review.

The push triggers a normal go-ci run on `develop`; that is expected, no action
needed.

## 7. Hotfix releases (patch on `main`, not yet exercised)

No hotfix has been shipped so far. When one is needed: branch from `main`
(`hotfix/vX.Y.Z`), land the fix + the same CHANGELOG/README sweep in one PR to
`main`, then follow steps 4–6 identically (tag on `main`, back-merge into
`develop`). Keep the squash-merge and annotated-tag rules.

## Version numbering

- **Patch** (`v0.4.Z+1`): fix-only or small-scope releases.
- **Minor** (`v0.Y.0`): feature releases (new instance kinds, API surface).
- Release notes are auto-generated from PR titles by
  `softprops/action-gh-release` — keep PR titles in conventional-commit shape
  (`feat(scope): …`, `fix(scope): …`) so the notes read well.

## Worked examples

| Release | Prep commit | Release PR | Tag commit on main |
|---|---|---|---|
| v0.4.2 | `a69ba7e` | #61 → `7707554` | `8247a4f` |
| v0.4.3 | `ccd6d95` | #77 → `a894b5d` | `a894b5d` |
