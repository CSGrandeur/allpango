// allpango 把备份送到网盘。第一个支持的盘是百度网盘开放平台。
// 只做备份需要的能力：登录、刷新令牌、列目录、容量、上传、目录同步。
// 不删除、不移动云端文件。
package main

import (
	"fmt"
	"os"
)

const usage = `allpango：把备份送到网盘（当前支持百度网盘）

用法:
  allpango login   --cred <凭证文件> --token <令牌文件>
  allpango refresh --cred <凭证文件> --token <令牌文件>
  allpango quota   --cred <凭证文件> --token <令牌文件>
  allpango ls      --cred <凭证文件> --token <令牌文件> --path <云端目录> [--json]
  allpango upload  --cred <凭证文件> --token <令牌文件> --local <本地文件> --path <云端文件路径>
  allpango sync    --cred <凭证文件> --token <令牌文件> --local <本地目录> --path <云端目录>
                   [--manifest <清单文件>] [--parallel N] [--seed | --seed-legacy]
  allpango verify  --cred <凭证文件> --token <令牌文件> --local <本地目录> --path <云端目录> [--json]

凭证文件是 JSON：{"app_key": "...", "app_secret": "..."}
令牌文件由 login 创建、由程序自动刷新写回，不要手工编辑。
云端路径必须在应用的 /apps/<应用名> 目录内。
程序不删除云端文件。退出码：成功 0，失败非 0。
`

func main() {
	if len(os.Args) < 2 {
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	cmd, args := os.Args[1], os.Args[2:]
	var err error
	switch cmd {
	case "login":
		err = runLogin(args)
	case "refresh":
		err = runRefresh(args)
	case "quota":
		err = runQuota(args)
	case "ls":
		err = runLs(args)
	case "upload":
		err = runUpload(args)
	case "sync":
		err = runSync(args)
	case "verify":
		err = runVerify(args)
	case "help", "-h", "--help":
		fmt.Print(usage)
		return
	default:
		fmt.Fprintf(os.Stderr, "未知命令: %s\n\n%s", cmd, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "错误: %v\n", err)
		os.Exit(1)
	}
}
