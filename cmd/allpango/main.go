// allpango 把备份送到网盘（支持百度网盘开放平台与腾讯微云）。
// 只做备份需要的能力：授权、列目录、上传、增量同步与核对。不删除、不移动云端文件。
package main

import (
	"fmt"
	"os"
)

const usage = `allpango：把备份送到网盘（支持百度网盘与腾讯微云）

直连用法（百度需 --cred + --token；微云只需 --token，login 时加 -p weiyun）:
  allpango login   [--cred <凭证> | -p weiyun] --token <令牌>
  allpango refresh [--cred <凭证>] --token <令牌>
  allpango quota   --cred <凭证> --token <令牌>
  allpango ls      [--cred <凭证>] --token <令牌> --path <云端目录> [--json]
  allpango upload  [--cred <凭证>] --token <令牌> --local <本地文件> --path <云端文件>
  allpango sync    [--cred <凭证>] --token <令牌> --local <本地目录> --path <云端目录>
                   [--manifest <清单>] [--parallel N] [--seed | --seed-legacy]
  allpango verify  [--cred <凭证>] --token <令牌> --local <本地目录> --path <云端目录>
                   [--size-only] [--json]

多网盘配置用法（-c/--config 指定配置文件，-r 指定名称或 --all 全选）:
  allpango remotes -c <配置文件> [--json]
  allpango ls      -c <配置文件> [-r <名称>] [--path <云端目录>]
  allpango sync    -c <配置文件> [-r <名称1,名称2> | --all] --local <本地目录>
  allpango verify  -c <配置文件> [-r <名称1,名称2> | --all] --local <本地目录>

百度路径须在 /apps/<应用名> 内；微云路径从网盘根起算（如 /备份/backup）。
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
	case "remotes":
		err = runRemotes(args)
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
