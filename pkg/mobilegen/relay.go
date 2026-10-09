package mobilegen

import "github.com/pi-dal/sakamoto/pkg/mobilerelay"

// SetTailscaleForceDERP is applied only after the old service has closed and
// before constructing a replacement. The iOS magicsock bind path reads it.
func SetTailscaleForceDERP(enabled bool) { mobilerelay.SetForceDERP(enabled) }
func TailscaleForceDERPEnabled() bool    { return mobilerelay.ForceDERP() }
