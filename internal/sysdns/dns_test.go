package sysdns

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type fakeSetup struct {
	servers              []string
	calls                [][]string
	failSet, failRestore bool
}

func (f *fakeSetup) run(args ...string) (string, error) {
	f.calls = append(f.calls, append([]string{}, args...))
	switch args[0] {
	case "-getdnsservers":
		if len(f.servers) == 0 {
			return "There aren't any DNS Servers set on Wi-Fi.\n", nil
		}
		return strings.Join(f.servers, "\n") + "\n", nil
	case "-setdnsservers":
		if f.failSet {
			return "", errors.New("synthetic administrative failure")
		}
		if args[2] == "Empty" {
			if f.failRestore {
				return "", errors.New("synthetic restore failure")
			}
			f.servers = []string{}
		} else {
			f.servers = append([]string{}, args[2:]...)
		}
		return "", nil
	default:
		return "", errors.New("unexpected command")
	}
}
func TestDNSTakeoverIsIdempotentAndRestoresExactState(t *testing.T) {
	for _, original := range [][]string{{}, {"9.9.9.9", "2620:fe::fe"}} {
		path := filepath.Join(t.TempDir(), StateName)
		setup := &fakeSetup{servers: append([]string{}, original...)}
		if err := Activate(path, "Wi-Fi", setup.run); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(setup.servers, []string{"127.0.0.1"}) {
			t.Fatal("loopback takeover failed")
		}
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != 0600 {
			t.Fatal("DNS snapshot is not private", err)
		}
		p, exists, err := savedDNS(path)
		if err != nil || !exists || !reflect.DeepEqual(p.Servers, original) {
			t.Fatal("original DNS state lost", err)
		}
		calls := len(setup.calls)
		if err := Activate(path, "Wi-Fi", setup.run); err != nil {
			t.Fatal(err)
		}
		if len(setup.calls) != calls+1 {
			t.Fatal("idempotent takeover should only read DNS")
		}
		if err := Restore(path, setup.run); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(setup.servers, original) {
			t.Fatal("original static/DHCP state not restored")
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("restored snapshot not removed")
		}
	}
}
func TestFailedDNSSetRollsBackWithoutLeavingSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), StateName)
	setup := &fakeSetup{servers: []string{}, failSet: true}
	if err := Activate(path, "Wi-Fi", setup.run); err == nil {
		t.Fatal("failed set accepted")
	}
	if len(setup.servers) != 0 {
		t.Fatal("failed set altered DNS")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("unneeded snapshot not cleaned up")
	}
}
func TestFailedRestoreRetainsSnapshotAndLoopback(t *testing.T) {
	path := filepath.Join(t.TempDir(), StateName)
	setup := &fakeSetup{servers: []string{}}
	if err := Activate(path, "Wi-Fi", setup.run); err != nil {
		t.Fatal(err)
	}
	setup.failRestore = true
	if err := Restore(path, setup.run); err == nil {
		t.Fatal("restore error lost")
	}
	if setup.servers[0] != "127.0.0.1" {
		t.Fatal("loopback resolver unexpectedly changed")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("recovery snapshot deleted on failure")
	}
}
func TestSnapshotSafetyAndExternalOverrides(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, StateName)
	secret := filepath.Join(dir, "synthetic-secret")
	if err := os.WriteFile(secret, []byte("never read"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, path); err != nil {
		t.Fatal(err)
	}
	setup := &fakeSetup{servers: []string{}}
	if err := Activate(path, "Wi-Fi", setup.run); err == nil {
		t.Fatal("snapshot symlink accepted")
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := Activate(path, "Wi-Fi", setup.run); err != nil {
		t.Fatal(err)
	}
	setup.servers = []string{"1.1.1.1"}
	if err := Restore(path, setup.run); err != nil {
		t.Fatal(err)
	}
	if setup.servers[0] != "1.1.1.1" {
		t.Fatal("manual DNS override was clobbered")
	}
	setup.servers = []string{"127.0.0.1"}
	if err := Activate(path, "Wi-Fi", setup.run); err == nil {
		t.Fatal("orphaned loopback DNS accepted without baseline")
	}
}
