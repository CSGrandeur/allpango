<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/images/banner-dark.svg">
    <img src="docs/images/banner-light.svg" alt="allpango: send your backups to the cloud, skip what hasn't changed" width="100%">
  </picture>
</p>

<p align="center">
  <a href="https://github.com/CSGrandeur/allpango/releases/latest"><img src="https://img.shields.io/github/v/release/CSGrandeur/allpango?style=flat-square&label=release&labelColor=2b2a27&color=4F7DFF" alt="Release"></a>
  <a href="go.mod"><img src="https://img.shields.io/badge/Go-1.22+-4F7DFF?style=flat-square&logo=go&logoColor=white&labelColor=2b2a27" alt="Go 1.22+"></a>
  <a href="#install"><img src="https://img.shields.io/badge/platform-Windows%20·%20macOS%20·%20Linux-4F7DFF?style=flat-square&labelColor=2b2a27" alt="Windows · macOS · Linux"></a>
  <a href="LICENSE"><img src="https://img.shields.io/github/license/CSGrandeur/allpango?style=flat-square&labelColor=2b2a27&color=4F7DFF" alt="MIT"></a>
</p>

<p align="center">
  <a href="#install">Install</a>&nbsp;&nbsp;·&nbsp;&nbsp;<a href="#log-in">Log in</a>&nbsp;&nbsp;·&nbsp;&nbsp;<a href="#usage">Usage</a>&nbsp;&nbsp;·&nbsp;&nbsp;<a href="#sync-and-verify">Sync and verify</a>&nbsp;&nbsp;·&nbsp;&nbsp;<a href="#limits">Limits</a>&nbsp;&nbsp;·&nbsp;&nbsp;<a href="#faq">FAQ</a>
  <br>
  <sub><a href="README.md">简体中文</a>&nbsp;&nbsp;|&nbsp;&nbsp;<b>English</b></sub>
</p>

<br>

allpango backs up a local directory to Baidu Netdisk. Each day's dumps go up; files that have not changed are not sent again. One static binary, no runtime dependencies.

It uploads and overwrites. There are no delete or move commands.

## Features

- Login uses a device code, so no callback URL is needed. The access token refreshes before it expires, and the new refresh token goes back into the file.
- Uploads follow the three official steps: precreate, slices, create. A same-name file is overwritten, not saved as `name(1)`. Past 1024 slices the slice size grows on its own.
- `sync` keeps a local manifest of what it has sent (size and mtime) and stops asking the disk about unchanged files. Delete the manifest and it gets slower, not wrong.
- `verify` ignores the manifest, lists the cloud directly and compares size and mtime file by file.
- Credentials, token and directories are all required; nothing is guessed. Exit 0 on success, non-zero otherwise.

## Install

Download a binary from [Releases](https://github.com/CSGrandeur/allpango/releases/latest). Builds are provided for Linux and macOS on amd64 / arm64 and Windows on amd64, with `SHA256SUMS.txt`.

Or install with Go 1.22+:

```bash
go install github.com/CSGrandeur/allpango/cmd/allpango@latest
```

## Log in

Create an app on the [Baidu Netdisk Open Platform](https://pan.baidu.com/union/console/applist) and put its keys in a credentials file:

```json
{ "app_key": "your AppKey", "app_secret": "your SecretKey" }
```

```bash
allpango login --cred cred.json --token token.json
```

The program prints a user code and a verification URL. Sign in to your Baidu account in a browser and enter the code; the token is written to `token.json` (mode 0600) and refreshed automatically afterwards. Credentials and tokens are read only from the files you name. They are never printed and never built in.

> [!NOTE]
> An app can only write under its own `/apps/<appname>` directory; the examples below use `myapp`. To upload into an existing backup directory, use the keys of the app that created it.

## Usage

```bash
auth="--cred cred.json --token token.json"
dir=/apps/myapp/backup

allpango quota  $auth
allpango ls     $auth --path $dir
allpango upload $auth --local ./db.sql.gz --path $dir/db.sql.gz
allpango sync   $auth --local ./backup --path $dir --manifest backup.mf.json
allpango verify $auth --local ./backup --path $dir
```

| Command | Purpose | Common flags |
| :-- | :-- | :-- |
| `login` | Device-code authorization, creates the token file | |
| `refresh` | Refresh the token manually | |
| `quota` | Total, used and free space | `--json` |
| `ls` | List a directory: name, size, mtime | `--path` `--json` |
| `upload` | Upload one file, overwriting the same name | `--local` `--path` |
| `sync` | Incremental sync of a whole directory | `--manifest` `--parallel` `--seed` `--seed-legacy` `--json` |
| `verify` | Per-file check against the cloud, non-zero exit on mismatch | `--size-only` `--json` |

Every command takes `--cred` and `--token`; add `-v` for per-file progress. `sync` uploads 4 files at a time by default; change it with `--parallel`.

## Sync and verify

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="docs/images/flow-en-dark.svg">
    <img src="docs/images/flow-en-light.svg" alt="allpango sync fan-out: backups compared against the manifest; 3,544 new or changed uploaded, 87,945 unchanged skipped, 401 empty files logged and skipped" width="100%">
  </picture>
</p>

The manifest records that a file was sent successfully once. It does not claim the cloud still has it. If the manifest is gone, or something else changed the cloud, `sync` precreates each file and lets the disk decide.

Change detection uses size and mtime, not hashes. A backup directory is mostly fresh dumps each day, so mtime is enough. Precreate carries the whole-file MD5, and the disk may accept identical content without the slices.

To take over a directory another tool uploaded, run `sync --seed` once. It rebuilds the manifest from the cloud listing, and files already there are not sent again. If that tool's timestamps do not match yours, use `--seed-legacy` to compare size only, and verify with `verify --size-only`.

A scheduled job can run `verify` after `sync`; it asks the cloud directly.

## Limits

- The per-file cap depends on the account: 4 GB normal, 10 GB member, 20 GB super member. At most 1024 slices.
- Empty files cannot go up; the API rejects them. They are logged and skipped, not counted as failures.
- The md5 in a listing is a cloud hash, not the file's real MD5. Do not compare it with a local one.
- Rapid upload is the disk's call. On some accounts identical content still goes up in slices; the saving then comes from the per-file requests you skip.

## FAQ

<details>
<summary><b>Where is the token stored?</b></summary>
<br>

In the file you pass to `--token`, mode 0600. A refresh writes a temp file and renames it. The refresh token is good for one use: each refresh replaces it and invalidates the old one, so do not edit the file by hand and do not let two processes share a copy.

</details>

<details>
<summary><b>What if the refresh token stops working?</b></summary>
<br>

Run `login` again. A failed refresh usually means the token is already invalid; retrying the old value will not help.

</details>

<details>
<summary><b>Will it delete remote files?</b></summary>
<br>

No. There are no delete or move commands in the program. Sync only deals with files that exist locally; anything extra on the cloud is left alone.

</details>

<details>
<summary><b>Does an interrupted sync re-upload everything?</b></summary>
<br>

No. The manifest is written after each file, so a re-run skips what already went up. The file that was cut off is precreated again, and the disk decides where to resume.

</details>

## Development

```bash
go test ./...
go build -o allpango ./cmd/allpango
```

<br>

<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="assets/mark_dark.svg">
    <img src="assets/icon.svg" width="36" alt="allpango">
  </picture>
  <br>
  <sub><a href="LICENSE">MIT</a> © 2026 CSGrandeur&nbsp;&nbsp;·&nbsp;&nbsp;<a href="CHANGELOG.md">Changelog</a></sub>
</p>
