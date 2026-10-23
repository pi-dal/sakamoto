// Package watchcontrol contains only the watcher control protocol, so clients
// need not link or start a watcher runtime to request bounded route tests.
package watchcontrol

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strings"
	"time"
)

const SocketName = "watch.sock"

// Request asks the independent watcher for fresh tests. The returned status is
// an acknowledgement, never verification of network recovery.
func Request(ctx context.Context, configPath string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	conn, err := (&net.Dialer{Timeout: 2 * time.Second}).DialContext(ctx, "unix", filepath.Join(filepath.Dir(configPath), SocketName))
	if err != nil {
		return "", fmt.Errorf("watcher unavailable: %w", err)
	}
	defer func() { _ = conn.Close() }()
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	deadline, _ := ctx.Deadline()
	if err := conn.SetDeadline(deadline); err != nil {
		return "", err
	}
	if _, err := fmt.Fprintln(conn, "recover"); err != nil {
		return "", err
	}
	line, err := bufio.NewReader(io.LimitReader(conn, 128)).ReadString('\n')
	if err != nil {
		return "", fmt.Errorf("read watcher acknowledgement: %w", err)
	}
	status := strings.TrimSpace(line)
	switch status {
	case "queued", "busy", "cooldown", "manual", "disabled", "unavailable", "maintenance":
		return status, nil
	default:
		return "", errors.New("unrecognized watcher acknowledgement")
	}
}
