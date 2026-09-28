package gen

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNodeEditDeletePreservesComments(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nodes.txt")
	a := "vless://11111111-2222-3333-4444-555555555555@203.0.113.10:443#East"
	b := "vless://11111111-2222-3333-4444-555555555555@203.0.113.11:443#West"
	if err := os.WriteFile(path, []byte("# manual\n"+a+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	entries, err := ReadNodes(path)
	if err != nil || len(entries) != 1 || entries[0].Line != 2 {
		t.Fatal(entries, err)
	}
	if err := ChangeNode(path, entries[0].Line, entries[0].Raw, b); err != nil {
		t.Fatal(err)
	}
	if err := ChangeNode(path, 2, a, a); err == nil {
		t.Fatal("stale edit should fail")
	}
	entries, _ = ReadNodes(path)
	if entries[0].Tag != "West" {
		t.Fatal(entries)
	}
	if err := ChangeNode(path, 0, "", a); err != nil {
		t.Fatal(err)
	}
	if err := ChangeNode(path, 2, b, ""); err != nil {
		t.Fatal(err)
	}
	content, _ := os.ReadFile(path)
	if !strings.Contains(string(content), "# manual\n") || strings.Contains(string(content), "#West") {
		t.Fatal("comments or deletion lost")
	}
}
