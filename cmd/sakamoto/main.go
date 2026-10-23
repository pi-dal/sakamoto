package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"os/user"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/pi-dal/sakamoto/internal/config"
	"github.com/pi-dal/sakamoto/internal/experiment"
	"github.com/pi-dal/sakamoto/internal/rescue"
	"github.com/pi-dal/sakamoto/internal/sbclient"
	"github.com/pi-dal/sakamoto/internal/security"
	"github.com/pi-dal/sakamoto/internal/svc"
	"github.com/pi-dal/sakamoto/internal/tui"
	"github.com/pi-dal/sakamoto/internal/watch"
)

const version = "0.2.21"

const usage = `sakamoto — a macOS sing-box controller inspired by Shadowrocket

Usage:
  sakamoto              Open the TUI (groups, tests, VPN, config and logs)
  sakamoto link         Repair the Homebrew command link; retain the old executable
  sakamoto link --check  Read-only check against the installed Homebrew executable
  sakamoto setup        Interactively install macOS daemon/watch services (no VPN start)
  sakamoto setup --check  Read-only service/config preflight (no changes)
  sakamoto service-update --check  Inspect the installed background service
  sakamoto service-update --reconnect  Update its executable with a controlled reconnect
  sakamoto daemon       Root supervisor (LaunchDaemon); manages sing-box and
                        accepts connect/disconnect commands on a Unix socket
  sakamoto watch        LaunchAgent for automatic proxy group fallback
  sakamoto rescue-install [--check | --replace]  Install/retain independent offline rescue
  sakamoto doctor       Diagnose local services and offline escape paths (read-only)
  sakamoto ctl state | status | info | logs | reload  Inspect/control the supervisor
  sakamoto ctl connect | disconnect     Explicit VPN lifecycle control
  sakamoto ctl resume | resume-installed | rollback  Manage blocked recovery
  sakamoto ctl urltest "TAG"             Queue a background latency check
  sakamoto ctl select "GROUP" "OUTBOUND"  Change a selector explicitly
  sakamoto learning     Show private learned domains and verification outcomes
  sakamoto repair       Detect and repair a crashed core; verify API and protected DNS
  sakamoto repair --check  Diagnose without changing the core or services
  sakamoto repair --restart  Explicitly reconnect a live but unhealthy core
  sakamoto repair --fallback [--restart]  Restore the verified working runtime
  sakamoto repair --regenerate [--restart]  Build and check configuration from sources
  sakamoto recover      Ask the user-level watcher for fresh MainProxy tests
  sakamoto dns-restore  Restore an orphaned DNS snapshot (core must be stopped)
  sakamoto dns-prepare  Build a private protected-DNS candidate; do not activate
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
  e           Edit config in TUI form (choose or edit fields)
  Esc         Back/cancel     q  Quit

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
	case "link":
		if len(args) > 2 || (len(args) == 2 && args[1] != "--check") {
			fatal("link", fmt.Errorf("only --check is supported"))
		}
		if err := runHomebrewLink(len(args) == 2); err != nil {
			fatal("link", err)
		}
	case "setup":
		if len(args) > 2 || (len(args) == 2 && args[1] != "--check") {
			fatal("setup", fmt.Errorf("only --check is supported; setup never enables reboot auto-connect"))
		}
		if err := runSetup(*cfgPath, len(args) == 2); err != nil {
			fatal("setup", err)
		}
	case "rescue-install":
		if len(args) > 2 || (len(args) == 2 && args[1] != "--check" && args[1] != "--replace") {
			fatal("rescue-install", fmt.Errorf("choose --check or --replace"))
		}
		option := ""
		if len(args) == 2 {
			option = args[1]
		}
		if err := runRescueInstall(*cfgPath, option); err != nil {
			fatal("rescue-install", err)
		}
	case "doctor":
		if len(args) != 1 {
			fatal("doctor", fmt.Errorf("doctor takes no arguments"))
		}
		report, err := rescue.Diagnose(ctx, filepath.Dir(*cfgPath))
		if err != nil {
			fatal("doctor", err)
		}
		report["cli_version"] = version
		encoder := json.NewEncoder(os.Stdout)
		encoder.SetIndent("", "  ")
		if err := encoder.Encode(report); err != nil {
			fatal("doctor", err)
		}
	case "daemon":
		if err := runDaemon(ctx); err != nil {
			fatal("daemon", err)
		}
	case "watch":
		if err := runWatch(ctx, *cfgPath); err != nil {
			fatal("watch", err)
		}
	case "dns-prepare":
		if len(args) != 1 {
			fatal("dns-prepare", fmt.Errorf("dns-prepare takes no arguments"))
		}
		candidate, err := security.PrepareDNS(*cfgPath)
		if err != nil {
			fatal("dns-prepare", err)
		}
		fmt.Println(candidate)
	case "dns-restore":
		if len(args) != 1 {
			fatal("dns-restore", fmt.Errorf("dns-restore takes no arguments"))
		}
		reply, err := svc.SendTo(filepath.Join(filepath.Dir(*cfgPath), svc.SockName), "dns-restore")
		if err != nil {
			fatal("dns-restore", err)
		}
		if !strings.HasPrefix(reply, "DNS restored") {
			fatal("dns-restore", fmt.Errorf("%s", strings.TrimSpace(reply)))
		}
		fmt.Println(strings.TrimSpace(reply))
	case "ctl":
		if len(args) < 2 {
			fatal("ctl", fmt.Errorf("choose state, status, logs, reload, urltest TAG, or select GROUP OUTBOUND"))
		}
		command := args[1]
		switch command {
		case "state", "status", "info", "logs", "reload", "connect", "disconnect", "resume", "resume-installed", "rollback", "maintenance":
			if len(args) != 2 {
				fatal("ctl", fmt.Errorf("%s takes no arguments", command))
			}
		case "urltest":
			if len(args) != 3 {
				fatal("ctl", fmt.Errorf("urltest needs a tag"))
			}
			raw, _ := json.Marshal(map[string]string{"tag": args[2]})
			command += " " + string(raw)
		case "select":
			if len(args) != 4 {
				fatal("ctl", fmt.Errorf("select needs a group and outbound"))
			}
			raw, _ := json.Marshal(map[string]string{"group": args[2], "outbound": args[3]})
			command += " " + string(raw)
		default:
			fatal("ctl", fmt.Errorf("unsupported command %s", command))
		}
		reply, err := svc.SendTo(filepath.Join(filepath.Dir(*cfgPath), svc.SockName), command)
		if err != nil {
			fatal("ctl", err)
		}
		if strings.HasPrefix(reply, "unknown:") {
			fatal("ctl", fmt.Errorf("running supervisor does not support %s; run sakamoto doctor and arrange a controlled supervisor upgrade", args[1]))
		}
		if strings.HasPrefix(reply, "error:") || strings.HasPrefix(reply, "start failed:") || strings.HasPrefix(reply, "disconnect failed:") {
			fatal("ctl", fmt.Errorf("%s", strings.TrimSpace(reply)))
		}
		fmt.Print(reply)
	case "learning":
		if len(args) != 1 {
			fatal("learning", fmt.Errorf("learning takes no arguments"))
		}
		if err := runLearningStatus(*cfgPath); err != nil {
			fatal("learning", err)
		}
	case "service-update":
		if err := runServiceUpdate(ctx, *cfgPath, args[1:]); err != nil {
			fatal("service-update", err)
		}
	case "repair":
		options := svc.RepairOptions{}
		repairFlags := flag.NewFlagSet("repair", flag.ContinueOnError)
		repairFlags.BoolVar(&options.CheckOnly, "check", false, "diagnose without changes")
		repairFlags.BoolVar(&options.Restart, "restart", false, "allow reconnecting a live core")
		repairFlags.BoolVar(&options.Fallback, "fallback", false, "restore the verified working runtime")
		repairFlags.BoolVar(&options.Regenerate, "regenerate", false, "build and check configuration from sources")
		if err := repairFlags.Parse(args[1:]); err != nil {
			fatal("repair", err)
		}
		if repairFlags.NArg() != 0 {
			fatal("repair", fmt.Errorf("repair takes no positional arguments"))
		}
		if err := options.Validate(); err != nil {
			fatal("repair", err)
		}
		if err := runRepair(ctx, *cfgPath, options); err != nil {
			fatal("repair", err)
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

func runLearningStatus(path string) error {
	cfg, err := config.Load(path)
	if err != nil {
		return err
	}
	dir := filepath.Dir(path)
	learned, err := experiment.Load(dir)
	if err != nil {
		return err
	}
	history, err := experiment.ReadReport(dir)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(struct {
		Mode              string                      `json:"mode"`
		CFRegionBlock     bool                        `json:"cf_region_block"`
		VerificationReady bool                        `json:"verification_ready"`
		Domains           []string                    `json:"learned_domains"`
		History           []experiment.LearningResult `json:"recent_verifications"`
	}{cfg.Experiment.Mode, cfg.Experiment.CFRegionBlock, experiment.ProbeReady(raw), learned.Domains, history})
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

func runDaemon(ctx context.Context) error {
	dir, err := daemonDirectory()
	if err != nil {
		return err
	}
	server := svc.NewServer(filepath.Join(dir, "config.json"), dir)
	server.SetVersion(version)
	return server.RunContext(ctx)
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
	w.SetVersion(version)
	go func() {
		for e := range w.Events() {
			fmt.Printf("[%s] %-6s %s\n", e.Time.Format("15:04:05"), e.Level, e.Message)
		}
	}()
	if err := w.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}
