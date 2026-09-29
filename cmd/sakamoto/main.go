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
	"github.com/pi-dal/sakamoto/internal/security"
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
  sakamoto rotate-api   仅暂存新密钥；下次从 TUI 正常连接时应用，不中断当前 TUN
  sakamoto rotate-api --apply-now  明确要求立即轮换（会短暂重连）
  sakamoto rotate-api --status | --cancel  查看/取消待应用轮换
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
	if err := fs.Parse(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	args := fs.Args()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	sub := "tui"
	if len(args) > 0 {
		sub = args[0]
	}
	switch sub {
	case "tui":
		if err := runTUI(ctx, *cfgPath); err != nil {
			fatal("tui", err)
		}
	case "daemon":
		if err := runDaemon(); err != nil {
			fatal("daemon", err)
		}
	case "watch":
		if err := runWatch(ctx, *cfgPath); err != nil {
			fatal("watch", err)
		}
	case "rotate-api":
		if len(args) > 2 {
			fatal("rotate-api", fmt.Errorf("只接受 --apply-now、--status 或 --cancel"))
		}
		option := ""
		if len(args) == 2 {
			option = args[1]
		}
		switch option {
		case "":
			if err := security.StageAPI(*cfgPath); err != nil {
				fatal("rotate-api", err)
			}
			fmt.Println("新 API 密钥已私有暂存；当前连接和 API 不变。下次从 TUI 正常连接时应用。")
		case "--apply-now":
			if err := security.RotateAPI(*cfgPath); err != nil {
				fatal("rotate-api", err)
			}
			fmt.Println("API 密钥已应用；连接中曾短暂重连。请重新打开 TUI。")
		case "--status":
			pending, err := security.PendingAPI(*cfgPath)
			if err != nil {
				fatal("rotate-api", err)
			}
			if pending {
				fmt.Println("API 轮换待应用；当前连接未改动")
			} else {
				fmt.Println("没有待应用的 API 轮换")
			}
		case "--cancel":
			if err := security.CancelPendingAPI(*cfgPath); err != nil {
				fatal("rotate-api", err)
			}
			fmt.Println("待应用轮换已取消；当前连接未改动")
		default:
			fatal("rotate-api", fmt.Errorf("未知选项 %q", option))
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

func fatal(subject string, err error) {
	fmt.Fprintln(os.Stderr, subject+":", err)
	os.Exit(1)
}

func runTUI(ctx context.Context, path string) error {
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	return tui.Run(ctx, cfg, path)
}

func daemonDirectory() (string, error) {
	home := os.Getenv("SAKAMOTO_HOME")
	if home == "" && os.Geteuid() == 0 {
		// The old plist did not define HOME; use the logged-in console user.
		if info, err := os.Stat("/dev/console"); err == nil {
			if st, ok := info.Sys().(*syscall.Stat_t); ok {
				if u, err := user.LookupId(fmt.Sprint(st.Uid)); err == nil && u.Uid != "0" {
					home = u.HomeDir
					if os.Getenv("SAKAMOTO_USER") == "" {
						if err := os.Setenv("SAKAMOTO_USER", u.Username); err != nil {
							return "", err
						}
					}
				}
			}
		}
	}
	if home == "" {
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			return "", err
		}
	}
	if home == "" || home == "/var/root" {
		return "", fmt.Errorf("无法确定登录用户目录；请在 plist 中设置 SAKAMOTO_HOME")
	}
	if dir := os.Getenv("SAKAMOTO_DIR"); dir != "" {
		return dir, nil
	}
	legacy := filepath.Join(home, ".config", "sakamoto")
	if _, err := os.Stat(filepath.Join(legacy, "sakamoto.yaml")); err == nil {
		return legacy, nil
	}
	return filepath.Join(home, ".sakamoto"), nil
}

func runDaemon() error {
	dir, err := daemonDirectory()
	if err != nil {
		return err
	}
	return svc.NewServer(filepath.Join(dir, "config.json"), dir).Run()
}

func runWatch(ctx context.Context, path string) error {
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	dial := func(ctx context.Context) (*sbclient.Client, error) {
		latest, err := config.Load(path)
		if err != nil {
			return nil, err
		}
		return sbclient.Dial(ctx, latest.API.URL, latest.API.Secret)
	}
	w := watch.New(cfg, dial)
	w.SetConfigPath(path)
	go func() {
		for e := range w.Events() {
			fmt.Printf("[%s] %-6s %s\n", e.Time.Format("15:04:05"), e.Level, e.Message)
		}
	}()
	if err := w.Run(ctx); err != nil && err != context.Canceled {
		return err
	}
	return nil
}
