package regionaldns_test

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pi-dal/sakamoto/pkg/regionaldns"
	"golang.org/x/net/dns/dnsmessage"
	"golang.org/x/net/proxy"
)

// A blocked regional relay must never deliver the scoped name to the default
// resolver. A control query proves the default resolver and counters are live.
func TestResolverIsolation(t *testing.T) {
	engine, err := exec.LookPath("sing-box")
	if err != nil {
		t.Skip("sing-box required")
	}
	upstream, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer upstream.Close()
	var forbidden, controls atomic.Int32
	go func() {
		buf := make([]byte, 2048)
		for {
			n, peer, e := upstream.ReadFrom(buf)
			if e != nil {
				return
			}
			var m dnsmessage.Message
			if m.Unpack(buf[:n]) != nil || len(m.Questions) != 1 {
				continue
			}
			if m.Questions[0].Name.String() == "control.example." {
				controls.Add(1)
			} else {
				forbidden.Add(1)
			}
			m.Response = true
			m.Answers = []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: m.Questions[0].Name, Class: dnsmessage.ClassINET, Type: dnsmessage.TypeA}, Body: &dnsmessage.AResource{A: [4]byte{192, 0, 2, 9}}}}
			b, _ := m.Pack()
			_, _ = upstream.WriteTo(b, peer)
		}
	}()
	socket, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := socket.Addr().(*net.TCPAddr).Port
	socket.Close()
	outbound := map[string]any{"type": "trojan", "tag": "Approved", "server": "192.0.2.10", "server_port": 443, "password": "fixture", "tls": map[string]any{"enabled": true, "server_name": "relay.example"}}
	root := map[string]any{"inbounds": []any{map[string]any{"type": "direct", "tag": "dns-in", "listen": "127.0.0.1", "listen_port": port}}, "route": map[string]any{"rules": []any{map[string]any{"inbound": "dns-in", "action": "hijack-dns"}}}, "outbounds": []any{outbound}, "dns": map[string]any{"servers": []any{map[string]any{"type": "udp", "tag": "normal", "server": "127.0.0.1", "server_port": upstream.LocalAddr().(*net.UDPAddr).Port}}, "final": "normal"}}
	mixed, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	mixedAddr := mixed.Addr().String()
	mixedPort := mixed.Addr().(*net.TCPAddr).Port
	mixed.Close()
	root["inbounds"] = append(root["inbounds"].([]any), map[string]any{"type": "mixed", "listen": "127.0.0.1", "listen_port": mixedPort})
	root["outbounds"] = append(root["outbounds"].([]any), map[string]any{"tag": "direct", "type": "direct"})
	route := root["route"].(map[string]any)
	route["final"] = "direct"
	route["default_domain_resolver"] = map[string]any{"server": "normal", "strategy": "ipv4_only"}
	raw, _ := json.Marshal(root)
	profile := regionaldns.Profile{Domains: "voice.example", ResolverIP: "192.0.2.12", TLSName: "dns.example", Relay: "Approved"}
	for _, bad := range []regionaldns.Profile{{Domains: "private.ts.net", ResolverIP: profile.ResolverIP, TLSName: profile.TLSName, Relay: profile.Relay}, {Domains: profile.Domains, ResolverIP: "dns.example", TLSName: profile.TLSName, Relay: profile.Relay}, {Domains: profile.Domains, ResolverIP: profile.ResolverIP, TLSName: profile.TLSName, Relay: "MainProxy"}} {
		if _, e := regionaldns.Apply(raw, bad, nil); e == nil {
			t.Fatal("unsafe configuration accepted")
		}
	}
	profile.RelayFingerprint, err = regionaldns.RelayFingerprint(raw, profile.Relay)
	if err != nil {
		t.Fatal(err)
	}
	for _, mutation := range []struct {
		k string
		v any
	}{{"server", "192.0.2.11"}, {"server_port", 8443}, {"tls", map[string]any{"enabled": true, "server_name": "replacement.example"}}} {
		original := outbound[mutation.k]
		outbound[mutation.k] = mutation.v
		changed, _ := json.Marshal(root)
		outbound[mutation.k] = original
		if _, e := regionaldns.Apply(changed, profile, nil); e == nil {
			t.Fatal("accepted changed relay identity")
		}
	}
	result, err := regionaldns.Apply(raw, profile, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = regionaldns.Verify(result, profile, nil); err != nil {
		t.Fatal(err)
	}
	if err = regionaldns.Verify(raw, profile, nil); err == nil {
		t.Fatal("accepted stale core without scoped DNS")
	}
	if err = json.Unmarshal(result, &root); err != nil {
		t.Fatal(err)
	}
	// Use a second native core as the approved relay and a private TLS fixture
	// as the recursive server. Both hops verify certificates.
	var resolveCount, leakedECS atomic.Int32
	doh := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, e := io.ReadAll(r.Body)
		var q dnsmessage.Message
		if e != nil || q.Unpack(body) != nil || len(q.Questions) != 1 || q.Questions[0].Name.String() != "voice.example." {
			http.Error(w, "unexpected query", 400)
			return
		}
		for _, record := range q.Additionals {
			if opt, ok := record.Body.(*dnsmessage.OPTResource); ok {
				for _, option := range opt.Options {
					if option.Code == 8 {
						leakedECS.Add(1)
					}
				}
			}
		}
		resolveCount.Add(1)
		q.Response = true
		q.Additionals = nil
		q.Answers = []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: q.Questions[0].Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}, Body: &dnsmessage.AResource{A: [4]byte{127, 0, 0, 1}}}}
		b, _ := q.Pack()
		w.Header().Set("Content-Type", "application/dns-message")
		_, _ = w.Write(b)
	}))
	var refuseTLS atomic.Bool
	var rejectedHandshakes atomic.Int32
	doh.TLS = &tls.Config{GetConfigForClient: func(*tls.ClientHelloInfo) (*tls.Config, error) {
		if refuseTLS.Load() {
			rejectedHandshakes.Add(1)
			return nil, errors.New("fixture handshake rejection")
		}
		return nil, nil
	}}
	doh.Config.ErrorLog = log.New(io.Discard, "", 0)
	doh.StartTLS()
	defer doh.Close()
	dir := t.TempDir()
	cert := string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: doh.Certificate().Raw}))
	keyBytes, keyErr := x509.MarshalPKCS8PrivateKey(doh.TLS.Certificates[0].PrivateKey)
	if keyErr != nil {
		t.Fatal(keyErr)
	}
	relaySocket, e := net.Listen("tcp4", "127.0.0.1:0")
	if e != nil {
		t.Fatal(e)
	}
	relayPort := relaySocket.Addr().(*net.TCPAddr).Port
	relaySocket.Close()
	relayConfig := map[string]any{"inbounds": []any{map[string]any{"type": "trojan", "listen": "127.0.0.1", "listen_port": relayPort, "users": []any{map[string]any{"password": "fixture"}}, "tls": map[string]any{"enabled": true, "certificate": cert, "key": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyBytes}))}}}, "outbounds": []any{map[string]any{"type": "direct"}}}
	relayRaw, _ := json.Marshal(relayConfig)
	relayPath := filepath.Join(dir, "relay.json")
	if e = os.WriteFile(relayPath, relayRaw, 0600); e != nil {
		t.Fatal(e)
	}
	relayCmd := exec.Command(engine, "run", "-c", relayPath)
	if e = relayCmd.Start(); e != nil {
		t.Fatal(e)
	}
	defer func() {
		if relayCmd.ProcessState == nil {
			_ = relayCmd.Process.Kill()
			_ = relayCmd.Wait()
		}
	}()
	relayDeadline := time.Now().Add(8 * time.Second)
	for {
		readiness, e := net.DialTimeout("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(relayPort)), 200*time.Millisecond)
		if e == nil {
			readiness.Close()
			break
		}
		if time.Now().After(relayDeadline) {
			t.Fatal("fixture relay failed to start")
		}
		time.Sleep(20 * time.Millisecond)
	}
	tls := map[string]any{"enabled": true, "server_name": "example.com", "certificate": cert}
	node := root["outbounds"].([]any)[0].(map[string]any)
	node["server"], node["server_port"], node["tls"] = "127.0.0.1", relayPort, tls
	servers := root["dns"].(map[string]any)["servers"].([]any)
	regional := servers[len(servers)-1].(map[string]any)
	regional["server"], regional["server_port"], regional["tls"] = "127.0.0.1", doh.Listener.Addr().(*net.TCPAddr).Port, tls
	// Map only the fixture's loopback application into the allowed-answer class.
	for _, v := range root["dns"].(map[string]any)["rules"].([]any) {
		r := v.(map[string]any)
		if r["ip_is_private"] == true {
			delete(r, "ip_is_private")
			r["ip_cidr"] = []string{"10.0.0.0/8"}
		}
	}
	app := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "direct fixture") }))
	defer app.Close()
	data, _ := json.Marshal(root)
	path := filepath.Join(dir, "fixture.json")
	if err = os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, engine, "run", "-c", path)
	cmd.Stderr = io.Discard // Native network discovery logs may contain local interface addresses.
	if err = cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	defer func() {
		cancel()
		_ = cmd.Wait()
	}()
	resolver := net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	}}
	deadline := time.Now().Add(5 * time.Second)
	for {
		probe, stop := context.WithTimeout(ctx, 250*time.Millisecond)
		_, err = resolver.LookupNetIP(probe, "ip4", "control.example.")
		stop()
		if err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("control query failed")
		}
		time.Sleep(30 * time.Millisecond)
	}
	// Send ECS deliberately and verify its absence at the authorized resolver.
	q := dnsmessage.Message{Header: dnsmessage.Header{ID: 24, RecursionDesired: true}, Questions: []dnsmessage.Question{{Name: dnsmessage.MustNewName("voice.example."), Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}}, Additionals: []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: dnsmessage.MustNewName("."), Type: dnsmessage.TypeOPT, Class: 1232}, Body: &dnsmessage.OPTResource{Options: []dnsmessage.Option{{Code: 8, Data: []byte{0, 1, 24, 0, 192, 0, 2}}}}}}}
	packet, _ := q.Pack()
	conn, e := net.Dial("udp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(port)))
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(4 * time.Second))
	_, _ = conn.Write(packet)
	buf := make([]byte, 2048)
	n, e := conn.Read(buf)
	var answer dnsmessage.Message
	if e != nil || answer.Unpack(buf[:n]) != nil || len(answer.Answers) != 1 || resolveCount.Load() != 1 || leakedECS.Load() != 0 {
		t.Fatalf("regional query failed: read_error=%t rcode=%d answers=%d approved_queries=%d ecs=%d", e != nil, answer.RCode, len(answer.Answers), resolveCount.Load(), leakedECS.Load())
	}
	dialer, err := proxy.SOCKS5("tcp", mixedAddr, nil, &net.Dialer{Timeout: 2 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	httpClient := &http.Client{Transport: &http.Transport{DialContext: dialer.(proxy.ContextDialer).DialContext, DisableKeepAlives: true}, Timeout: 3 * time.Second}
	target := "http://voice.example:" + strconv.Itoa(app.Listener.Addr().(*net.TCPAddr).Port)
	response, err := httpClient.Get(target)
	if err != nil {
		t.Fatal("SOCKS domain dial did not reach direct application")
	}
	body, err := io.ReadAll(response.Body)
	response.Body.Close()
	if err != nil || string(body) != "direct fixture" || resolveCount.Load() < 2 {
		t.Fatal("SOCKS domain dial did not use approved DNS")
	}
	checkFailure := func(stage string) {
		t.Helper()
		if r, e := httpClient.Get(target); e == nil {
			r.Body.Close()
			t.Fatalf("SOCKS succeeded during %s", stage)
		}
		probe, stop := context.WithTimeout(ctx, 4*time.Second)
		defer stop()
		if _, e := resolver.LookupNetIP(probe, "ip4", "voice.example."); e == nil {
			t.Fatalf("DNS succeeded during %s", stage)
		}
	}
	dotted, err := httpClient.Get("http://voice.example.:" + strconv.Itoa(app.Listener.Addr().(*net.TCPAddr).Port))
	if err != nil {
		t.Fatal("absolute DNS name bypassed approved resolution")
	}
	dotted.Body.Close()
	refuseTLS.Store(true)
	doh.CloseClientConnections()
	checkFailure("TLS failure")
	if rejectedHandshakes.Load() == 0 {
		t.Fatal("fixture did not exercise TLS failure")
	}
	if e := relayCmd.Process.Kill(); e != nil {
		t.Fatal(e)
	}
	_ = relayCmd.Wait()
	checkFailure("relay outage")
	if forbidden.Load() != 0 || controls.Load() == 0 {
		t.Fatal("default resolver received a protected query or control failed")
	}
	t.Logf("unauthorized queries=%d, forwarded ECS=%d, approved answers=%d", forbidden.Load(), leakedECS.Load(), resolveCount.Load())
}
