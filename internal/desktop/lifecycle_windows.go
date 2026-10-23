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

func runtimeConfigurationRepair(context.Context, string, bool) (HealthReport, error) {
	return HealthReport{State: "unavailable", Summary: "The external service manages configuration recovery."}, errExternalLifecycle
}

func runtimeRecoveryStatus(context.Context, string) (bool, bool, bool) { return false, false, false }
func runtimeStatus(context.Context, string) (string, error)            { return "", errExternalLifecycle }
func runtimeDisconnect(context.Context, string) (string, error)        { return "", errExternalLifecycle }
func runtimeConnect(string) (string, error)                            { return "", errExternalLifecycle }
