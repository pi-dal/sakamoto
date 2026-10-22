//go:build darwin || linux

package desktop

import (
	"context"
	"path/filepath"

	"github.com/pi-dal/sakamoto/internal/security"
	"github.com/pi-dal/sakamoto/internal/svc"
)

func runtimeStatus(ctx context.Context, path string) (string, error) {
	return svc.SendToContext(ctx, filepath.Join(filepath.Dir(path), svc.SockName), "status")
}
func runtimeDisconnect(ctx context.Context, path string) (string, error) {
	return svc.SendToContext(ctx, filepath.Join(filepath.Dir(path), svc.SockName), "disconnect")
}
func runtimeConnect(path string) (string, error) {
	reply, _, err := security.ConnectWithPending(path)
	return reply, err
}
