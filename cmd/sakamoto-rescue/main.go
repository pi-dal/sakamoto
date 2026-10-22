// sakamoto-rescue deliberately excludes the TUI, libbox, subscriptions, and API
// clients. Keep a signed copy installed locally before a failure happens.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"syscall"

	"github.com/pi-dal/sakamoto/internal/rescue"
)

var version = "development"

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "rescue:", err)
		os.Exit(1)
	}
}
func run() error {
	if len(os.Args) < 2 {
		return fmt.Errorf("choose diagnose, exit --restore-network, shell, or version")
	}
	command := os.Args[1]
	if command == "version" {
		fmt.Println("sakamoto-rescue", version)
		return nil
	}
	flags := flag.NewFlagSet("sakamoto-rescue", flag.ContinueOnError)
	dir := flags.String("dir", "", "absolute runtime directory (required)")
	restore := flags.Bool("restore-network", false, "explicitly exit VPN protection and restore original network settings")
	if err := flags.Parse(os.Args[2:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("unexpected positional arguments")
	}
	if command == "shell" {
		if os.Geteuid() == 0 {
			return fmt.Errorf("open the clean rescue shell as your ordinary user, without sudo")
		}
		return syscall.Exec("/bin/zsh", []string{"zsh", "-f"}, rescue.CleanEnvironment(os.Environ()))
	}
	if *dir == "" || !filepath.IsAbs(*dir) {
		return fmt.Errorf("supply --dir /absolute/path/to/runtime; the rescue tool never guesses a root user's home")
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	switch command {
	case "diagnose":
		if *restore {
			return fmt.Errorf("diagnose is read-only; --restore-network only applies to exit")
		}
		report, err := rescue.Diagnose(context.Background(), *dir)
		if err != nil {
			return err
		}
		report["rescue_version"] = version
		return encoder.Encode(report)
	case "exit":
		if !*restore {
			return fmt.Errorf("explicit --restore-network is required: ordinary networking may use your original network resolver")
		}
		system, err := rescue.NativeSystem(*dir)
		if err != nil {
			return err
		}
		outcome, exitErr := rescue.Exit(context.Background(), *dir, system)
		if err = encoder.Encode(outcome); err != nil {
			return err
		}
		return exitErr
	default:
		return fmt.Errorf("unknown rescue action %q", command)
	}
}
