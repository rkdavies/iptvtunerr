# Repo map (navigation)

First place to look before editing. Keeps agents from thrashing.

**This folder is the Plex Tuner project.** There are two projects × two hosts = 4 repos. Push only to **origin** and **plex** from here.

## Remotes (do not cross-push)

| Remote    | Repo         | Host   | Use from this folder      |
|-----------|--------------|--------|----------------------------|
| **origin** | plexTuner    | GitLab | ✓ Push Plex Tuner here    |
| **plex**   | plexTuner    | GitHub | ✓ Push Plex Tuner here    |
| **github** | repoTemplate | GitHub | ✗ Do not push from here   |
| **template** | repoTemplate | GitLab | ✗ Do not push from here   |

To push Plex Tuner to both: `git push origin main && git push plex main`. Never `git push github` or `git push template` from this folder.

## Main entrypoints

| Path | Purpose |
|------|--------|
| **`cmd/plex-tuner/main.go`** | Plex Tuner app: flags, index (M3U/player_api), catalog, HTTP (lineup, stream), optional VODFS mount. |
| **`internal/indexer/`** | M3U stream parsing, player_api (auth, live, VOD, series with parallel fetch). |
| **`internal/catalog/`** | Movie/Series/LiveChannel types; Save (snapshot then encode), Load. |
| **`internal/vodfs/`** | FUSE: root, Movies/TV dirs, virtual files (NodeOpener, keep FD). |
| **`AGENTS.md`** | Agent instructions; **`memory-bank/`** = state + process. |
| **`docs/index.md`** | Doc map (Diátaxis). |

## Single binary (supervisor vs oracle)

**There is only one application:** `plex-tuner` (one binary, one build). All modes are subcommands of that binary:

- `run`, `serve`, `index` — single tuner or catalog refresh
- `supervise` — read a JSON config and spawn N child processes (each child is the same binary, e.g. `plex-tuner run -addr=:5004 ...`)
- `plex-epg-oracle` — CLI to probe Plex HDHR wizard/channelmap and write reports (one-shot or cron)
- `probe`, `mount`, `vod-split`, `plex-vod-register`, `epg-link-report` — other subcommands

**Single-pod consolidation (done):** Main and oracle instances run in **one** supervisor pod. The main supervisor config (ConfigMap `plextuner-supervisor-config`) includes both the main instances (hdhr-main, categories, …) and the oracle-cap instances (hdhrcap100…hdhrcap600). Service `plextuner-oracle-hdhr` selects `app=plextuner-supervisor` and exposes ports 5201–5206. There is no separate `plextuner-oracle-supervisor` deployment. Oracle instance definitions for merging into a generated config: `k8s/oracle-instances.json`. Windows/macOS: one `go build`; no extra binaries.

**Category DVR feeds (dvr-*.m3u):** Category instances (bcastus, newsus, generalent, …) use M3U URLs like `http://iptv-m3u-server.plex.svc/dvr-bcastus.m3u`. Those files are produced by **iptv-m3u-server** (split step) in the sibling `k3s/plex` repo. They must emit **all** stream URLs per channel (not just one), or after `PLEX_TUNER_STRIP_STREAM_HOSTS` every channel is dropped and guides show "no live channels available". See known_issues.md (Category DVRs … 0 channels) and runbook §10.

## Key modules

- **`internal/httpclient`** — Shared tuned HTTP client; used by indexer, gateway, materializer, vodfs.
- **`internal/materializer`** — Download: single GET or range (16 MiB, 206 when off>0); env `PLEX_TUNER_RANGE_DOWNLOAD=1`.
- **`internal/gateway`** — Proxy `/stream?url=...` to upstream.
- **`internal/probe`** — Lineup (lineup.json), discovery (device.xml).

## No-go zones

- **`.env`** — Never commit; never log or echo. Credentials live only in env.
- **Generated/vendor** — Don't edit unless the task explicitly requires it.
- **Weakening tests** — Don't "fix" by loosening assertions; fix root cause.

## Verification and QA

- **`scripts/verify`** — Full check: format (gofmt) → vet → test → build. Fail fast, same as CI.
- **`scripts/quick-check.sh`** — Tests only; use for short feedback when iterating.
- **Troubleshooting:** [docs/runbooks/plextuner-troubleshooting.md](docs/runbooks/plextuner-troubleshooting.md) — fail-fast checklist, probe, logs, common failures.
- CI runs only `scripts/verify`.
