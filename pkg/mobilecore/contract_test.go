package mobilecore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/pi-dal/sakamoto/internal/core"
)

// The iOS integration consumes this package's state vocabulary through
// ios/contract/vocabulary.json. That file is a checked-in golden generated
// from the internal/core and mobilecore constants below; the Swift side
// (ios/Sources/SakamotoKit) asserts its enums against the same file. Renaming
// or removing a vocabulary value therefore fails on both toolchains before
// any frontend can drift from the macOS TUI semantics.
//
// Regenerate the golden after an intentional vocabulary change:
//
//	SAKAMOTO_UPDATE_CONTRACT=1 go test ./pkg/mobilecore -run TestVocabularyContract
//
// then run the normal test to confirm, and review the Swift-side diff.
type vocabularyContract struct {
	ContractVersion  int      `json:"contractVersion"`
	Source           string   `json:"source"`
	ServiceStates    []string `json:"serviceStates"`
	SessionPhases    []string `json:"sessionPhases"`
	ProbeStates      []string `json:"probeStates"`
	ProbePaths       []string `json:"probePaths"`
	RoutingModes     []string `json:"routingModes"`
	RoutingModeCycle []string `json:"routingModeCycle"`
	NodeStatuses     []string `json:"nodeStatuses"`
	ConfigStates     []string `json:"configStates"`
	ConfigEvents     []string `json:"configEvents"`
	NoticeKinds      []string `json:"noticeKinds"`
}

func buildVocabularyContract() vocabularyContract {
	return vocabularyContract{
		ContractVersion: 1,
		Source:          "github.com/pi-dal/sakamoto/pkg/mobilecore",
		ServiceStates: []string{
			string(core.ServiceStopped),
			string(core.ServiceStarting),
			string(core.ServiceRunning),
			string(core.ServiceStopping),
			string(core.ServiceUnavailable),
		},
		SessionPhases: []string{
			string(core.PhaseDisconnected),
			string(core.PhaseStarting),
			string(core.PhaseTUNRunning),
			string(core.PhaseReachable),
			string(core.PhaseUnverified),
			string(core.PhaseConflict),
			string(core.PhaseUnavailable),
			string(core.PhaseStopping),
		},
		ProbeStates: []string{
			string(core.ProbeIdle),
			string(core.ProbeChecking),
			string(core.ProbeReachable),
			string(core.ProbeUnverified),
		},
		ProbePaths: []string{
			core.PathSystem,
			core.PathProxy,
			core.PathBoth,
			core.PathSystemOnly,
			core.PathProxyOnly,
		},
		RoutingModes: []string{
			string(core.ModeRule),
			string(core.ModeGlobal),
			string(core.ModeDirect),
		},
		// The cycle core.NextRoutingMode implements: Rule -> Global -> Direct
		// -> Rule; an unknown current value starts at Rule. Recorded as data
		// for display only; the cycle itself must be driven through the
		// gomobile bridge (mobilecore.NextRoutingMode), never re-implemented.
		RoutingModeCycle: []string{
			string(core.ModeRule),
			string(core.ModeGlobal),
			string(core.ModeDirect),
		},
		NodeStatuses: []string{
			string(core.NodeUntested),
			string(core.NodeTesting),
			string(core.NodeReachable),
			string(core.NodeFailed),
		},
		ConfigStates: []string{
			string(core.ConfigClean),
			string(core.ConfigNeedsRegenerate),
			string(core.ConfigNeedsReconnect),
		},
		ConfigEvents: []string{
			EventModified,
			EventRegenerateSucceeded,
			EventRegenerateFailed,
			EventApplied,
		},
		NoticeKinds: []string{
			string(core.NoticeInfo),
			string(core.NoticeProgress),
			string(core.NoticeSuccess),
			string(core.NoticeWarning),
			string(core.NoticeError),
		},
	}
}

const vocabularyContractUpdateEnv = "SAKAMOTO_UPDATE_CONTRACT"

func TestVocabularyContractGolden(t *testing.T) {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed; cannot locate the golden file")
	}
	// pkg/mobilecore/contract_test.go -> ios/contract/vocabulary.json
	goldenPath := filepath.Join(filepath.Dir(thisFile), "..", "..", "ios", "contract", "vocabulary.json")

	want, err := json.MarshalIndent(buildVocabularyContract(), "", "  ")
	if err != nil {
		t.Fatalf("marshal contract: %v", err)
	}
	want = append(want, '\n')

	if os.Getenv(vocabularyContractUpdateEnv) == "1" {
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
			t.Fatalf("create contract directory: %v", err)
		}
		if err := os.WriteFile(goldenPath, want, 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		t.Logf("wrote %s", goldenPath)
		return
	}

	got, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("read golden %s: %v (regenerate with %s=1)", goldenPath, err, vocabularyContractUpdateEnv)
	}
	if string(got) != string(want) {
		t.Fatalf(`vocabulary contract drifted from ios/contract/vocabulary.json.

The Swift frontend (ios/Sources/SakamotoKit) is locked to this golden; if the
change is intentional, regenerate and review both sides:

  %s=1 go test ./pkg/mobilecore -run TestVocabularyContract
  cd ios && swift test

--- want (Go) ---
%s
--- got (ios/contract/vocabulary.json) ---
%s
`, vocabularyContractUpdateEnv, want, got)
	}
}
