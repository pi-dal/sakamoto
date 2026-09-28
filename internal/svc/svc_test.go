package svc

import (
	"os"
	"path/filepath"
	"testing"
)

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
