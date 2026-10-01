package svc

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCommandReplyErrorPreservesTransportCause(t *testing.T) {
	transport := errors.New("synthetic socket failure")
	if err := commandReplyError("connect", " denied\n", transport); !errors.Is(err, transport) || !strings.Contains(err.Error(), "denied") {
		t.Fatal("lost supervisor reply or transport error", err)
	}
	if err := commandReplyError("disconnect", "not running\n", nil); !strings.Contains(err.Error(), "not running") || strings.Contains(err.Error(), "<nil>") {
		t.Fatal("unexpected supervisor reply was misreported", err)
	}
}

func TestSingBoxExecutableOverride(t *testing.T) {
	t.Setenv("SAKAMOTO_SING_BOX", "/custom/homebrew/bin/sing-box")
	if got := singBoxExecutable(); got != "/custom/homebrew/bin/sing-box" {
		t.Fatalf("sing-box path override ignored: %q", got)
	}
}

func TestValidateAPIServiceFailClosed(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	for _, tc := range []struct {
		json  string
		valid bool
	}{
		{`{"services":[{"type":"api","listen":"127.0.0.1","secret":"0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef","dashboard":{"enabled":false}}]}`, true},
		{`{"services":[{"type":"api","listen":"127.0.0.1","secret":"change-me"}]}`, false},
		{`{"services":[{"type":"api","listen":"0.0.0.0","secret":"0123456789abcdef0123456789abcdef"}]}`, false},
		{`{"services":[{"type":"api","listen":"127.0.0.1","secret":"0123456789abcdef0123456789abcdef","dashboard":{"enabled":true}}]}`, false},
		{`{"services":[]}`, false},
	} {
		if err := os.WriteFile(path, []byte(tc.json), 0600); err != nil {
			t.Fatal(err)
		}
		if err := validateAPIService(path); (err == nil) != tc.valid {
			t.Fatalf("validation for %s: %v", tc.json, err)
		}
	}
}
