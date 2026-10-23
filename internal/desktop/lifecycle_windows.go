//go:build windows

package desktop

import (
	"context"
	"errors"
)

var errExternalLifecycle = errors.New("Windows lifecycle is managed by an external core service; enable external-core mode")

func runtimeRepair(context.Context, string, bool, bool) (HealthReport, error) {
	return HealthReport{State: "unavailable", Summary: "The external service manages core recovery."}, errExternalLifecycle
}

func runtimeStatus(context.Context, string) (string, error)     { return "", errExternalLifecycle }
func runtimeDisconnect(context.Context, string) (string, error) { return "", errExternalLifecycle }
func runtimeConnect(string) (string, error)                     { return "", errExternalLifecycle }
