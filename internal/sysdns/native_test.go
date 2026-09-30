package sysdns

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

func forwardTestSOCKS(conn net.Conn) {
	defer func() { _ = conn.Close() }()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	var greeting [2]byte
	if _, err := io.ReadFull(conn, greeting[:]); err != nil {
		return
	}
	if greeting[0] != 5 {
		return
	}
	if _, err := io.CopyN(io.Discard, conn, int64(greeting[1])); err != nil {
		return
	}
	if _, err := conn.Write([]byte{5, 0}); err != nil {
		return
	}
	var request [4]byte
	if _, err := io.ReadFull(conn, request[:]); err != nil || request[1] != 1 || request[3] != 1 {
		return
	}
	var destination [6]byte
	if _, err := io.ReadFull(conn, destination[:]); err != nil {
		return
	}
	ip := net.IP(destination[:4])
	if !ip.IsLoopback() {
		return
	} // Tests never connect to public IPs.
	port := int(destination[4])<<8 | int(destination[5])
	upstream, err := net.DialTimeout("tcp", net.JoinHostPort(ip.String(), fmt.Sprint(port)), time.Second)
	if err != nil {
		return
	}
	defer func() { _ = upstream.Close() }()
	if _, err := conn.Write([]byte{5, 0, 0, 1, 127, 0, 0, 1, 0, 0}); err != nil {
		return
	}
	done := make(chan struct{})
	go func() { _, _ = io.Copy(upstream, conn); close(done) }()
	_, _ = io.Copy(conn, upstream)
	_ = conn.Close()
	_ = upstream.Close()
	<-done
}

func TestNativeDNSUDPAndTCPUseCertificateVerifiedProxyDoH(t *testing.T) {
	if _, err := exec.LookPath("sing-box"); err != nil {
		t.Skip("sing-box integration dependency")
	}
	var dohQueries, proxyConnections atomic.Int32
	doh := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var data []byte
		var err error
		if r.Method == "GET" {
			data, err = base64.RawURLEncoding.DecodeString(r.URL.Query().Get("dns"))
		} else {
			data, err = io.ReadAll(r.Body)
		}
		if err != nil {
			http.Error(w, "invalid query", 400)
			return
		}
		var message dnsmessage.Message
		if err := message.Unpack(data); err != nil || len(message.Questions) != 1 {
			http.Error(w, "invalid DNS", 400)
			return
		}
		dohQueries.Add(1)
		message.Response = true
		message.RCode = dnsmessage.RCodeSuccess
		message.RecursionAvailable = true
		message.Answers = []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: message.Questions[0].Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 1}, Body: &dnsmessage.AResource{A: [4]byte{93, 184, 215, 14}}}}
		response, err := message.Pack()
		if err != nil {
			http.Error(w, "DNS packing failed", 500)
			return
		}
		w.Header().Set("Content-Type", "application/dns-message")
		_, _ = w.Write(response)
	}))
	defer doh.Close()
	socks, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = socks.Close() }()
	go func() {
		for {
			conn, err := socks.Accept()
			if err != nil {
				return
			}
			proxyConnections.Add(1)
			go forwardTestSOCKS(conn)
		}
	}()
	portListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := portListener.Addr().(*net.TCPAddr).Port
	if err := portListener.Close(); err != nil {
		t.Fatal(err)
	}
	certificate := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: doh.Certificate().Raw}))
	core := map[string]any{
		"log":       map[string]any{"level": "error"},
		"inbounds":  []any{map[string]any{"type": "direct", "tag": "protected-dns", "listen": "127.0.0.1", "listen_port": port}},
		"outbounds": []any{map[string]any{"type": "socks", "tag": "exit", "version": "5", "server": "127.0.0.1", "server_port": socks.Addr().(*net.TCPAddr).Port}},
		"dns":       map[string]any{"servers": []any{map[string]any{"type": "https", "tag": "remote", "server": "127.0.0.1", "server_port": doh.Listener.Addr().(*net.TCPAddr).Port, "detour": "exit", "tls": map[string]any{"enabled": true, "server_name": "example.com", "certificate": []string{certificate}}}}, "final": "remote", "disable_cache": true},
		"route":     map[string]any{"rules": []any{map[string]any{"inbound": []string{"protected-dns"}, "action": "hijack-dns"}}, "default_domain_resolver": map[string]any{"server": "remote"}},
	}
	data, err := json.Marshal(core)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "native-dns.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sing-box", "run", "-c", path)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	address := net.JoinHostPort("127.0.0.1", fmt.Sprint(port))
	for ctx.Err() == nil {
		if err := Probe(address); err == nil {
			if dohQueries.Load() < 2 || proxyConnections.Load() < 1 {
				t.Fatal("queries did not pass through proxy DoH")
			}
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("native UDP/TCP DNS did not become healthy with trusted TLS/proxy")
}
