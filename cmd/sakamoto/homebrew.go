package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type homebrewLink struct {
	brew, destination, target string
}

func inspectHomebrewLink(ctx context.Context) (homebrewLink, error) {
	brew, err := exec.LookPath("brew")
	if err != nil {
		return homebrewLink{}, errors.New("homebrew is unavailable in PATH")
	}
	prefixFor := func(args ...string) (string, error) {
		output, err := exec.CommandContext(ctx, brew, args...).Output()
		if err != nil {
			return "", fmt.Errorf("brew %s failed: %w", strings.Join(args, " "), err)
		}
		path := strings.TrimSpace(string(output))
		if !filepath.IsAbs(path) || strings.ContainsAny(path, "\r\n") {
			return "", errors.New("homebrew returned an invalid absolute prefix")
		}
		return filepath.Clean(path), nil
	}
	prefix, err := prefixFor("--prefix")
	if err != nil {
		return homebrewLink{}, err
	}
	keg, err := prefixFor("--prefix", "sakamoto")
	if err != nil {
		return homebrewLink{}, err
	}
	target := filepath.Join(keg, "bin", "sakamoto")
	info, err := os.Stat(target)
	if err != nil {
		return homebrewLink{}, fmt.Errorf("installed sakamoto executable unavailable: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0 {
		return homebrewLink{}, errors.New("installed sakamoto target is not an executable file")
	}
	return homebrewLink{brew, filepath.Join(prefix, "bin", "sakamoto"), target}, nil
}

func (link homebrewLink) current() bool {
	target, err := os.Stat(link.target)
	if err != nil {
		return false
	}
	current, err := os.Stat(link.destination)
	return err == nil && os.SameFile(target, current)
}

func (link homebrewLink) repair(ctx context.Context, checkOnly bool) (string, error) {
	if link.current() {
		return "", nil
	}
	if checkOnly {
		return "", fmt.Errorf("homebrew command %s does not name the installed sakamoto; run sakamoto link to repair", link.destination)
	}
	var backup string
	info, err := os.Lstat(link.destination)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return "", err
	}
	if err == nil {
		if !info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
			return "", errors.New("refusing to replace a directory or special file at the Homebrew command path")
		}
		// Keep the backup beside the original: relative symlink targets remain
		// valid, and rename does not copy or expose a former executable.
		file, err := os.CreateTemp(filepath.Dir(link.destination), ".sakamoto-link-backup-*")
		if err != nil {
			return "", fmt.Errorf("create executable backup: %w", err)
		}
		backup = file.Name()
		if err := file.Close(); err != nil {
			_ = os.Remove(backup)
			return "", err
		}
		if err := os.Rename(link.destination, backup); err != nil {
			_ = os.Remove(backup)
			return "", fmt.Errorf("retain old executable: %w", err)
		}
	}
	rollback := func(cause error) (string, error) {
		if backup != "" {
			if err := os.Rename(backup, link.destination); err != nil {
				return backup, fmt.Errorf("%w; restore failed: %v; old executable retained at %s", cause, err, backup)
			}
			return "", fmt.Errorf("%w; previous executable restored", cause)
		}
		// The command did not exist before repair. Remove a partial link
		// created by brew so failed attempts retain that original state.
		if info, err := os.Lstat(link.destination); err == nil {
			if !info.Mode().IsRegular() && info.Mode()&os.ModeSymlink == 0 {
				return "", fmt.Errorf("%w; unexpected object remains at %s", cause, link.destination)
			}
			if err := os.Remove(link.destination); err != nil {
				return "", fmt.Errorf("%w; partial command cleanup failed: %v", cause, err)
			}
		} else if !errors.Is(err, os.ErrNotExist) {
			return "", fmt.Errorf("%w; cannot inspect partial command: %v", cause, err)
		}
		return "", cause
	}
	for _, args := range [][]string{{"unlink", "sakamoto"}, {"link", "sakamoto"}} {
		if err := exec.CommandContext(ctx, link.brew, args...).Run(); err != nil {
			return rollback(fmt.Errorf("brew %s failed: %w", strings.Join(args, " "), err))
		}
	}
	if !link.current() {
		return rollback(errors.New("homebrew link command returned without repairing the executable"))
	}
	return backup, nil
}

func runHomebrewLink(checkOnly bool) error {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	link, err := inspectHomebrewLink(ctx)
	if err != nil {
		return err
	}
	backup, err := link.repair(ctx, checkOnly)
	if err != nil {
		return err
	}
	fmt.Printf("Homebrew command verified: %s\n", link.destination)
	if backup != "" {
		fmt.Printf("Previous executable retained: %s\n", backup)
	}
	return nil
}

// Source builds keep their own executable. A setup launched from a Homebrew
// keg repairs its stable command before writing a launchd executable path.
func setupHomebrewBinary(binary string, checkOnly bool) (string, error) {
	resolved, err := filepath.EvalSymlinks(binary)
	if err != nil {
		return "", err
	}
	if !strings.Contains(filepath.ToSlash(resolved), "/Cellar/sakamoto/") {
		return binary, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	link, err := inspectHomebrewLink(ctx)
	if err != nil {
		return "", err
	}
	backup, err := link.repair(ctx, checkOnly)
	if err != nil {
		return "", err
	}
	if backup != "" {
		fmt.Printf("Homebrew command repaired; previous executable retained at %s\n", backup)
	}
	return link.destination, nil
}
