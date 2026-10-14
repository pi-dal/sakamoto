package sourcesync

import (
	"reflect"
	"testing"
)

func TestGroupSourcesValidateCanonicalizeMergeAndClear(t *testing.T) {
	for _, invalid := range []string{`null`, `[]`, `{"Custom":[]}`, `{"OthersAuto":null}`, `{"OthersAuto":["B","B"]}`, `{"OthersAuto":[""]}`, `{"OthersAuto":["bad\nname"]}`, `{"OthersAuto":[42]}`} {
		if err := (Bundle{Version: 1, Files: map[string]string{GroupsFile: invalid}}).Validate(); err == nil {
			t.Fatalf("accepted invalid group policy: %s", invalid)
		}
	}
	local := Canonical(Bundle{Version: 1, Files: map[string]string{GroupsFile: ` {"RealityAuto":["R3","R2"],"OthersAuto":[]} `}})
	want := `{"RealityAuto":["R2","R3"]}`
	if local.Files[GroupsFile] != want {
		t.Fatal("group policy not canonical", local.Files)
	}
	fresh := Bundle{Version: 1, Files: map[string]string{}}
	merged, downloads, err := Merge(fresh, local, Baseline{})
	if err != nil || !reflect.DeepEqual(downloads, []string{GroupsFile}) || merged.Files[GroupsFile] != want {
		t.Fatal(merged, downloads, err)
	}
	base := baseline(local, "target")
	clear := Canonical(Bundle{Version: 1, Files: map[string]string{GroupsFile: `{"RealityAuto":[]}`}})
	merged, downloads, err = Merge(local, clear, base)
	if err != nil || merged.Files[GroupsFile] != "{}" || len(downloads) != 1 {
		t.Fatal("clear not adopted", merged, downloads, err)
	}
	changed := Bundle{Version: 1, Files: map[string]string{GroupsFile: `{"RealityAuto":["R4"]}`}}
	if _, _, err := Merge(changed, clear, base); err == nil {
		t.Fatal("concurrent policy edits must conflict")
	}
}
