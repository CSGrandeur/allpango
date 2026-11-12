# panpost

panpost is a command-line program that uploads files through the current [Baidu Netdisk Open Platform](https://pan.baidu.com/union/doc/) API, into that app's own `/apps/{appname}` directory.

It is shaped for backups: refresh a token, list a directory, upload a file, and skip files that have not changed. It is not a graphical netdisk client and it does not bypass download limits.

The repository is open. Login, list, and upload are not implemented yet. The `panpost` command only prints a short notice and exits.

The intended flow for an upload is precreate, upload the slices named in the response, then create the file. An empty slice list from precreate means the content is not uploaded again. Tokens and app secrets are read from a file the caller names. They are not compiled in and not printed. The program does not delete remote files.

A third-party app can write only under `/apps/{appname}`. A normal account is limited to 4GB per file and 1024 slices.

```bash
go build -o panpost ./cmd/panpost
```

[MIT](LICENSE)
