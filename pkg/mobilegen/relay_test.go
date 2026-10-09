package mobilegen

import "testing"

func TestDERPPolicyDoesNotLatchAcrossReconnects(t *testing.T) {
	defer SetTailscaleForceDERP(false)
	for i := 0; i < 100; i++ {
		SetTailscaleForceDERP(true)
		if !TailscaleForceDERPEnabled() {
			t.Fatal("DERP policy did not enable")
		}
		SetTailscaleForceDERP(false)
		if TailscaleForceDERPEnabled() {
			t.Fatal("DERP policy remained enabled for the next profile")
		}
	}
}
