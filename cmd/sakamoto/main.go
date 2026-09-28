package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"syscall"

	"github.com/pi-dal/sakamoto/internal/config"
	"github.com/pi-dal/sakamoto/internal/sbclient"
	"github.com/pi-dal/sakamoto/internal/svc"
	"github.com/pi-dal/sakamoto/internal/tui"
	"github.com/pi-dal/sakamoto/internal/watch"
)

const version = "0.1.0"

const usage = `sakamoto — Shadowrocket 复刻版 sing-box 控制面

用法:
  sakamoto              打开 TUI（默认；切组/测速/开关/配置/日志全在这里）
  sakamoto daemon       root 监督进程（LaunchDaemon 拉起；管 sing-box 子进程，
                        提供 connect/disconnect socket —— 等价 SR 的 VPN 开关）
  sakamoto watch        fallback 降级守护（LaunchAgent；链首优先自动降级回切）
  sakamoto version      版本
  sakamoto help         本说明

TUI 快捷键:
  tab/1-4   Home/Config/Data/Settings 四页（对齐 SR 四 Tab）
  j/k       移动     enter  选中/切换    t  测延迟
  c/空格    连接或断开                  u  全部测速
  a         Config 页导入 conf URL/路径  g  更新并生成配置
  e         编辑配置文件               Esc 返回/取消  q  退出

节点维护: 往 ~/.sakamoto/nodes.txt 丢分享链接（SR 导出格式兼容），TUI 里 g 重建
`

func main() {
	fs := flag.NewFlagSet("sakamoto", flag.ExitOnError)
	cfgPath := fs.String("config", config.DefaultPath(), "config file path")
	fs.Parse(os.Args[1:])
	args := fs.Args()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	sub := "tui"
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "tui":
		cfg, err := config.Load(*cfgPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "config:", err)
			os.Exit(1)
		}
		if err := tui.Run(ctx, cfg, *cfgPath); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	case "daemon": // root 监督进程：等价 SR VPN 后台（LaunchDaemon 专用）
		home := os.Getenv("SAKAMOTO_HOME")
		if home == "" && os.Geteuid() == 0 {
			// 兼容旧 plist：launchd 不给 root 设置 HOME，以 /dev/console 识别当前登录用户。
			if stat, err := os.Stat("/dev/console"); err == nil {
				if st, ok := stat.Sys().(*syscall.Stat_t); ok {
					if u, err := user.LookupId(fmt.Sprint(st.Uid)); err == nil && u.Uid != "0" {
						home = u.HomeDir
						if os.Getenv("SAKAMOTO_USER") == "" {
							os.Setenv("SAKAMOTO_USER", u.Username)
						}
					}
				}
			}
		}
		if home == "" {
			home, _ = os.UserHomeDir()
		}
		if home == "" || home == "/var/root" {
			fmt.Fprintln(os.Stderr, "daemon: 无法确定登录用户目录；在 plist 中设置 SAKAMOTO_HOME")
			os.Exit(1)
		}
		dir := os.Getenv("SAKAMOTO_DIR")
		if dir == "" {
			// Old LaunchDaemon has no explicit directory: keep its state in place even
			// after an unexpected restart. The new template sets SAKAMOTO_DIR explicitly.
			legacy := filepath.Join(home, ".config", "sakamoto")
			if _, err := os.Stat(filepath.Join(legacy, "sakamoto.yaml")); err == nil {
				dir = legacy
			} else {
				dir = filepath.Join(home, ".sakamoto")
			}
		}
		s := svc.NewServer(dir+"/config.json", dir)
		if err := s.Run(); err != nil {
			fmt.Fprintln(os.Stderr, "daemon:", err)
			os.Exit(1)
		}
	case "watch":
		cfg, err := config.Load(*cfgPath)
		if err != nil {
			fmt.Fprintln(os.Stderr, "config:", err)
			os.Exit(1)
		}
		dial := func(ctx context.Context) (*sbclient.Client, error) {
			return sbclient.Dial(ctx, cfg.API.URL, cfg.API.Secret)
		}
		w := watch.New(cfg, dial)
		w.SetConfigPath(*cfgPath)
		go func() {
			for e := range w.Events() {
				fmt.Printf("[%s] %-6s %s\n", e.Time.Format("15:04:05"), e.Level, e.Message)
			}
		}()
		if err := w.Run(ctx); err != nil && err != context.Canceled {
			fmt.Fprintln(os.Stderr, "watch:", err)
			os.Exit(1)
		}
	case "version":
		fmt.Println("sakamoto", version)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n\n%s", sub, usage)
		os.Exit(2)
	}
}
