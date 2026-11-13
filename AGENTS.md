# AGENTS.md

## This repository is a private-use fork — never contribute upstream

This is `nomanoma121/longhorn-scheduler`, a fork of `naver/longhorn-scheduler`
used only to build container images for a personal homelab.
**There is zero intention of sending anything back to upstream.**

### Hard rules

- **Never open a pull request, issue, comment, or review against `naver/longhorn-scheduler`.**
- **Never push to the `upstream` remote.** It is read-only (fetch only).
- `gh pr create` in a fork targets the *parent* repo by default. Always pass the
  repo explicitly:

  ```bash
  gh pr create --repo nomanoma121/longhorn-scheduler --base main ...
  ```

- If any command or tool would interact with `naver/*` in a write capacity,
  stop and ask the user instead.

### Remotes

| remote     | repo                          | use                  |
|------------|-------------------------------|----------------------|
| `origin`   | `nomanoma121/longhorn-scheduler` | push / PRs here only |
| `upstream` | `naver/longhorn-scheduler`    | fetch only (sync)    |

Pulling in upstream changes:

```bash
git fetch upstream
git merge upstream/main   # into local main, then push to origin
```

## Build & CI

- Go module path stays `github.com/naver/longhorn-scheduler` (don't rename; it
  keeps upstream merges painless).
- `go build ./... && go vet ./... && go test ./... -race`
- `.github/workflows/ci.yml` — build / vet / test on PRs and `main`.
- `.github/workflows/image.yml` — multi-arch (amd64/arm64) image to
  `ghcr.io/nomanoma121/longhorn-scheduler`.
  - `main` push → `latest`, `main`, `sha-<short>`
  - `v*` tag push → `X.Y.Z`, `X.Y`
  - PRs → build only, no push
- Helm chart in `helm/` defaults to the GHCR image above.
