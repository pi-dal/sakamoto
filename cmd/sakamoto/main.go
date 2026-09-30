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

const version = "0.2.4"

const usage = `sakamoto — a macOS sing-box controller inspired by Shadowrocket

Usage:
  sakamoto              Open the TUI (groups, tests, VPN, config and logs)
  sakamoto daemon       Root supervisor (LaunchDaemon); manages sing-box and
                        accepts connect/disconnect commands on a Unix socket
  sakamoto watch        LaunchAgent for automatic proxy group fallback
  sakamoto recover      Ask the user-level watcher for fresh MainProxy tests
  sakamoto rotate-api   Stage a private API key; apply on the next TUI connect
  sakamoto rotate-api --apply-now  Rotate immediately (brief reconnect)
  sakamoto rotate-api --status | --cancel  Check/cancel a staged rotation
  sakamoto version      Print the version
  sakamoto help         Show this help

TUI keys:
  tab / 1–5   Home / Config / Data / Settings / About
  j / k       Move       enter  Select      t  Test latency
  c / space   Connect or disconnect       u  Test all nodes
  m           Cycle Rule / Global / Direct (new connections)
  a           Import conf URL/path in Config  g  Regenerate config
  e           Edit config     Esc  Back/cancel   q  Quit

Add share links to ~/.sakamoto/nodes.txt and press g in the TUI to regenerate.
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
	case "recover":
		if len(args) != 1 {
			fatal("recover", fmt.Errorf("recover takes no arguments"))
		}
		status, err := watch.RequestRecovery(*cfgPath)
		if err != nil {
			fatal("recover", err)
		}
		fmt.Println(status)
	case "rotate-api":
		if len(args) > 2 {
			fatal("rotate-api", fmt.Errorf("only --apply-now, --status, or --cancel is supported"))
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
			fmt.Println("New API key staged privately; the active connection and API are unchanged. It applies on the next TUI connect.")
		case "--apply-now":
			if err := security.RotateAPI(*cfgPath); err != nil {
				fatal("rotate-api", err)
			}
			fmt.Println("API key applied after a brief reconnect. Reopen any TUI already running.")
		case "--status":
			pending, err := security.PendingAPI(*cfgPath)
			if err != nil {
				fatal("rotate-api", err)
			}
			if pending {
				fmt.Println("API rotation pending; the active connection is unchanged")
			} else {
				fmt.Println("No pending API rotation")
			}
		case "--cancel":
			if err := security.CancelPendingAPI(*cfgPath); err != nil {
				fatal("rotate-api", err)
			}
			fmt.Println("Pending rotation canceled; the active connection is unchanged")
		default:
			fatal("rotate-api", fmt.Errorf("unknown option %q", option))
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
		return "", fmt.Errorf("cannot determine the logged-in user's home; set SAKAMOTO_HOME in the plist")
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
