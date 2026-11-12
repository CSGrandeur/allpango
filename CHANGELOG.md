# Changelog

All notable changes to allpango are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project adheres
to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

Entries describe user-facing changes only.

## [Unreleased]

### Changed
- Redesigned the sync diagram in the README (pending review).

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

[0.1.0]: https://github.com/CSGrandeur/allpango/releases/tag/v0.1.0
