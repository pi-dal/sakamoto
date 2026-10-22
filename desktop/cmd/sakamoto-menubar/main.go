package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"

	"github.com/pi-dal/sakamoto/desktop/internal/menubar"
	"github.com/pi-dal/sakamoto/internal/config"
)

func main() {
	fs := flag.NewFlagSet("sakamoto-menubar", flag.ExitOnError)
	path := fs.String("config", "", "Existing sakamoto.yaml (defaults to the active runtime)")
	demo := fs.Bool("demo", false, "Use labeled demo data; never read or control the live runtime")
	show := fs.Bool("show", false, "Open the panel when the app starts")
	external := fs.Bool("external-core", runtime.GOOS != "darwin", "Use an existing core API; its service owns tunnel lifecycle")
	verify := fs.String("verify", "", "Capture a packaged demo session and save its verification receipt")
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "unexpected arguments")
		os.Exit(2)
	}
	if *verify != "" && !*demo {
		fmt.Fprintln(os.Stderr, "--verify requires --demo")
		os.Exit(2)
	}
	if *verify != "" {
		if err := os.MkdirAll(*verify, 0o755); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		// A previous successful receipt must not hide a failed current run.
		if err := os.Remove(filepath.Join(*verify, "native-app.json")); err != nil && !os.IsNotExist(err) {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
	if *path == "" && !*demo {
		*path = config.DefaultPath()
	}
	if *path != "" {
		absolute, err := filepath.Abs(*path)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		*path = absolute
	}
	if err := menubar.Run(menubar.AppOptions{ConfigPath: *path, ExternalCore: *external, Demo: *demo, Show: *show, VerifyDirectory: *verify}); err != nil {
		fmt.Fprintln(os.Stderr, "Sakamoto could not start:", err)
		os.Exit(1)
	}
	if *verify != "" {
		raw, err := os.ReadFile(filepath.Join(*verify, "native-app.json"))
		var receipt struct {
			Passed bool `json:"passed"`
		}
		if err != nil || json.Unmarshal(raw, &receipt) != nil || !receipt.Passed {
			fmt.Fprintln(os.Stderr, "Native verification failed; inspect native-app.json")
			os.Exit(1)
		}
	}
}
