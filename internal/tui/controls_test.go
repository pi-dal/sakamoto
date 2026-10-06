package tui

import (
	"github.com/pi-dal/sakamoto/internal/config"
	"strings"
	"testing"
)

func TestPolicyControlsHoverAndClickUseRenderedRows(t *testing.T) {
	m := testModel(t)
	m.page = configPage
	m.configDetail = policySection
	m.height = 24
	m.cfg.PolicyRules = []config.PolicyRule{{Match: "infini.money", Action: "proxy"}}
	view := m.View()
	var add, edit, remove hit
	for _, h := range m.hits {
		switch h.action {
		case "policy-add":
			add = h
		case "policy-edit":
			edit = h
		case "policy-delete":
			remove = h
		}
	}
	if add.action == "" || edit.action == "" || remove.action == "" {
		t.Fatalf("policy buttons missing: %+v", m.hits)
	}
	lines := strings.Split(view, "\n")
	for _, h := range []hit{add, edit, remove} {
		if h.y >= len(lines) || !strings.Contains(lines[h.y], "[") {
			t.Fatalf("hit row %d does not render a button: %q", h.y, lines[h.y])
		}
	}
	m.hoverX, m.hoverY = add.x0+1, add.y
	m.View()
	if !m.hovered("policy-add", 0) {
		t.Fatal("policy add hover not detected")
	}
	m.click(add.x0+1, add.y)
	if !m.policyEditing {
		t.Fatal("policy add click did not open pane")
	}
	m.View()
	var cancel hit
	for _, h := range m.hits {
		if h.action == "policy-cancel" {
			cancel = h
			break
		}
	}
	if cancel.action == "" {
		t.Fatal("policy cancel missing")
	}
	m.hoverX, m.hoverY = cancel.x0+1, cancel.y
	m.View()
	if !strings.Contains(strings.Join(strings.Split(m.View(), "\n"), "\n"), "Cancel") {
		t.Fatal("policy cancel not rendered")
	}
	m.click(cancel.x0+1, cancel.y)
	if m.policyEditing {
		t.Fatal("policy cancel click did not close pane")
	}
}

func TestConfigActionButtonsStayClickableAfterEmptySourceSection(t *testing.T) {
	m := testModel(t)
	m.page = configPage
	m.height = 24
	m.cfg.Subscriptions = nil
	m.View()
	var importHit, editHit hit
	for _, h := range m.hits {
		switch h.action {
		case "import":
			importHit = h
		case "edit-config":
			editHit = h
		}
	}
	if importHit.action == "" || editHit.action == "" {
		t.Fatal("config actions missing")
	}
	lines := strings.Split(m.View(), "\n")
	for _, h := range []hit{importHit, editHit} {
		if h.y >= len(lines) || !strings.Contains(lines[h.y], "[") {
			t.Fatalf("action row mismatch: %d %q", h.y, lines[h.y])
		}
	}
	m.hoverX, m.hoverY = editHit.x0+1, editHit.y
	m.View()
	if !m.hovered("edit-config", 0) {
		t.Fatal("edit settings hover not detected")
	}
	m.click(editHit.x0+1, editHit.y)
	if !m.configForm {
		t.Fatal("edit settings click did not open pane")
	}
}
