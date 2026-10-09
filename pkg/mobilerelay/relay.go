// Package mobilerelay supplies the iOS Tailscale DERP policy shared by the
// gomobile bridge and the pinned magicsock iOS adapter. No process environment
// mutation or additional Go runtime is involved.
package mobilerelay

import "sync/atomic"

var forceDERP atomic.Bool

func SetForceDERP(enabled bool) { forceDERP.Store(enabled) }
func ForceDERP() bool           { return forceDERP.Load() }
