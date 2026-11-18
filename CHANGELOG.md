# Changelog

All notable changes to allpango are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres
to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Entries describe user-facing changes only.

## [Unreleased]

## [0.2.0] - 2026-11-18

### Added
- Support for Tencent Weiyun via the official MCP HTTP interface: streaming SHA-1 state verification, 512 KB chunked uploads, rapid-upload detection, directory caching, token pasting/verification (`login -p weiyun`), and auto-detection from `--token` files containing `mcp_token`.
- Multi-remote configuration (`-c`/`--config`, `-r`/`--remote`, `--all`) and `remotes` command to list targets and sync/verify multiple clouds in one invocation.
- Manifest scope binding (`provider` + `remote_root`) to prevent accidentally reusing a manifest across providers or target directories.

### Changed
- Redesigned the sync diagram and tidied the README layout: install via `go install`, a flag column in the command table, and a section on taking over another tool's uploads.

## [0.1.0] - 2026-11-12

First release.

### Added
- Device-code login to Baidu Netdisk (`login`), no callback URL; access token refreshes before expiry, new refresh token written back atomically.
- Quota (`quota`) and directory listing (`ls`), with `--json`.
- Single-file upload (`upload`): precreate, slices, create; overwrites the same name without producing `name(1)`; slice size grows automatically past the count limit.
- Incremental directory sync (`sync`): a local manifest skips unchanged files; empty files are recorded and skipped; interrupted runs resume.
- Manifest rebuild from the cloud listing (`sync --seed` / `--seed-legacy`) to take over another tool's uploads without re-sending.
- Per-file verification (`verify`) against the cloud, independent of the manifest; `--size-only` for legacy timestamp semantics.

### Notes
- Never deletes or moves remote files.
- Credentials and tokens are read only from caller-specified files; never printed, never built in.

[0.2.0]: https://github.com/CSGrandeur/allpango/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/CSGrandeur/allpango/releases/tag/v0.1.0
