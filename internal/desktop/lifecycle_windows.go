//go:build windows

package desktop

import (
	"context"
	"errors"
)

var errExternalLifecycle = errors.New("Windows lifecycle is managed by an external core service; enable external-core mode")

func runtimeStatus(context.Context, string) (string, error)     { return "", errExternalLifecycle }
func runtimeDisconnect(context.Context, string) (string, error) { return "", errExternalLifecycle }
func runtimeConnect(string) (string, error)                     { return "", errExternalLifecycle }
