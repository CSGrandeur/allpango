# allpango

allpango is a command-line program that sends backups to cloud disks. The first disk is the [Baidu Netdisk Open Platform](https://pan.baidu.com/union/doc/). Other disks can be added later, selected by name, through the same command.

It is shaped for backups: refresh a token, list a directory, upload a file, and skip files that have not changed. It is not a graphical client and it does not bypass download limits.

The repository is open. Login, list, and upload are not implemented yet. The `allpango` command only prints a short notice and exits. Only the Baidu store is planned for the first commands.

The intended Baidu upload is precreate, upload the slices named in the response, then create the file. An empty slice list from precreate means the content is not uploaded again. Tokens and app secrets are read from a file the caller names. They are not compiled in and not printed. The program does not delete remote files.

A Baidu third-party app can write only under `/apps/{appname}`. A normal account is limited to 4GB per file and 1024 slices.

```bash
go build -o allpango ./cmd/allpango
```

[MIT](LICENSE)
