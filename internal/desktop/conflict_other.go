//go:build windows || linux

package desktop

import "context"

// The macOS VPN client has no corresponding service on these platforms.
func ShadowrocketConnected(context.Context) bool { return false }
