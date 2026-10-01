package security

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pi-dal/sakamoto/internal/config"
	"github.com/sagernet/sing-box/daemon"
)

func TestSupervisorConnectedFailsClosedOnUnknownStatus(t *testing.T) {
	// Darwin's Unix socket path limit is shorter than Go's verbose t.TempDir.
	dir, err := os.MkdirTemp("/tmp", "dns-prepare-sock-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	path := filepath.Join(dir, "svc.sock")
	if connected, err := supervisorConnected(path); err != nil || connected {
		t.Fatalf("absent supervisor: connected=%v err=%v", connected, err)
	}
	for _, tc := range []struct {
		name, reply string
		want        bool
		wantErr     bool
	}{
		{"connected", "connected pid=123 dns=off\n", true, false},
		{"disconnected", "disconnected\n", false, false},
		{"garbled", "unknown: status\n", false, true},
		{"closed", "", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			listener, err := net.Listen("unix", path)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = listener.Close(); _ = os.Remove(path) }()
			finished := make(chan struct{})
			go func() {
				defer close(finished)
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				defer func() { _ = conn.Close() }()
				buffer := make([]byte, 32)
				_, _ = conn.Read(buffer)
				_, _ = conn.Write([]byte(tc.reply))
			}()
			connected, err := supervisorConnected(path)
			<-finished
			if connected != tc.want || (err != nil) != tc.wantErr {
				t.Fatalf("connected=%v err=%v, want %v / error=%v", connected, err, tc.want, tc.wantErr)
			}
		})
	}
}

func TestPrepareSelectorsRefusesActiveCoreWithoutAPI(t *testing.T) {
	dir, err := os.MkdirTemp("/tmp", "dns-prepare-active-")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.RemoveAll(dir) }()
	listener, err := net.Listen("unix", filepath.Join(dir, "svc.sock"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		buffer := make([]byte, 32)
		_, _ = conn.Read(buffer)
		_, _ = conn.Write([]byte("connected pid=123 dns=off\n"))
	}()
	cfg := config.Default()
	cfg.API.URL = "http://127.0.0.1:1" // Guaranteed unavailable native API.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err = preserveSelectors(ctx, filepath.Join(dir, "sakamoto.yaml"), dir, cfg)
	<-finished
	if err == nil || !strings.Contains(err.Error(), "cannot safely preserve selectors") {
		t.Fatal("active selector loss was accepted", err)
	}
}

func TestApplySelectorChoicesPreservesCurrentSelection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	before := []byte(`{"outbounds":[{"type":"selector","tag":"MainProxy","outbounds":["A","B"],"default":"A"},{"type":"direct","tag":"direct"}],"route":{"final":"MainProxy"}}`)
	if err := os.WriteFile(path, before, 0600); err != nil {
		t.Fatal(err)
	}
	selected := &daemon.Groups{Group: []*daemon.Group{{Tag: "MainProxy", Selected: "B"}}}
	if err := applySelectorChoices(path, selected); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var core struct {
		Outbounds []struct {
			Default string `json:"default"`
		} `json:"outbounds"`
		Route struct {
			Final string `json:"final"`
		} `json:"route"`
	}
	if err := json.Unmarshal(data, &core); err != nil || core.Outbounds[0].Default != "B" || core.Route.Final != "MainProxy" {
		t.Fatalf("selector or unrelated route changed: %s (%v)", data, err)
	}
	selected.Group[0].Selected = "missing"
	if err := applySelectorChoices(path, selected); err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatal("missing live selection was silently discarded", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || string(after) != string(data) {
		t.Fatal("failed selector capture modified the candidate", err)
	}
}

func TestApplySelectorChoicesRejectsMalformedGeneratedConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	for _, data := range []string{
		`{"outbounds":{}}`,
		`{"outbounds":[true]}`,
		`{"outbounds":[{"type":"selector","tag":"MainProxy"}]}`,
		`{"outbounds":[{"type":"selector","tag":"MainProxy","outbounds":[{}]}]}`,
	} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
		err := applySelectorChoices(path, &daemon.Groups{Group: []*daemon.Group{{Tag: "MainProxy", Selected: "A"}}})
		if err == nil {
			t.Fatalf("malformed candidate accepted: %s", data)
		}
	}
}

func TestWriteCandidateInputsDoesNotIgnorePolicyReadErrors(t *testing.T) {
	root := t.TempDir()
	cfg := config.Default()
	path := filepath.Join(root, "sakamoto.yaml")
	candidate := filepath.Join(root, "candidate")
	if err := os.Mkdir(candidate, 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeCandidateInputs(path, candidate, cfg); err != nil {
		t.Fatalf("missing optional policy: %v", err)
	}
	if err := os.Mkdir(filepath.Join(root, "auto-proxy.json"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := writeCandidateInputs(path, candidate, cfg); err == nil || !strings.Contains(err.Error(), "read learned proxy policy") {
		t.Fatal("policy read error was ignored", err)
	}
}
