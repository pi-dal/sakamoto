package mobilelocation

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"encoding/json"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func fixture() []byte {
	coordinate := binary.AppendUvarint(nil, 8)
	coordinate = binary.AppendUvarint(coordinate, 100)
	coordinate = binary.AppendUvarint(coordinate, 16)
	coordinate = binary.AppendUvarint(coordinate, 200)
	coordinate = binary.AppendUvarint(coordinate, 24)
	coordinate = binary.AppendUvarint(coordinate, 25)
	coordinate = append(coordinate, 0x38, 0x09) // unrelated field must survive
	device := encodeField(field{number: 1, kind: 2}, []byte("aa:bb:cc:dd:ee:ff"))
	device = append(device, encodeField(field{number: 2, kind: 2}, coordinate)...)
	payload := encodeField(field{number: 2, kind: 2}, device)
	frame := []byte{0, 1, 0, 0, 0, 1, 0, 0}
	frame = binary.BigEndian.AppendUint16(frame, uint16(len(payload)))
	return append(frame, payload...)
}
func testSettings() Settings {
	return Settings{Enabled: true, Latitude: -33.5, Longitude: 151.25, Accuracy: 15}
}
func settingsJSON(s Settings) string { b, _ := json.Marshal(s); return string(b) }

func TestCoordinatesAndUnknownFieldsSurvive(t *testing.T) {
	updated, count, err := patchBody(fixture(), testSettings())
	if err != nil || count != 1 {
		t.Fatalf("patch: %d %v", count, err)
	}
	top, _ := decodeMessage(updated[10:])
	device, _ := decodeMessage(top[0].data)
	point, _ := decodeMessage(device[1].data)
	lat, _ := binary.Uvarint(point[0].data)
	lon, _ := binary.Uvarint(point[1].data)
	if int64(lat) != -3350000000 || int64(lon) != 15125*1_000_000 || !bytes.Equal(point[3].encoded, []byte{0x38, 0x09}) {
		t.Fatalf("incorrect coordinates or lost unknown field: %v", point)
	}
	if int(binary.BigEndian.Uint16(updated[8:])) != len(updated)-10 {
		t.Fatal("envelope length not adjusted")
	}
}
func TestARPCAndGzip(t *testing.T) {
	payload := fixture()[10:]
	body := []byte{0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 1}
	body = binary.BigEndian.AppendUint32(body, uint32(len(payload)))
	body = append(body, payload...)
	body = append(body, 0x42, 0x77)
	var zipped bytes.Buffer
	z := gzip.NewWriter(&zipped)
	_, _ = z.Write(body)
	_ = z.Close()
	result, count, err := patchBody(zipped.Bytes(), testSettings())
	if err != nil || count != 1 || !bytes.HasSuffix(result, []byte{0x42, 0x77}) {
		t.Fatalf("ARPC gzip: %d %v", count, err)
	}
}
func TestMalformedAndExpandedBodiesAreRejected(t *testing.T) {
	for _, data := range [][]byte{nil, []byte("not location data"), {0, 1, 0, 0, 0, 1, 0, 0, 255, 255}} {
		if _, _, err := patchBody(data, testSettings()); err == nil {
			t.Fatal("accepted unknown envelope")
		}
	}
	var zipped bytes.Buffer
	z := gzip.NewWriter(&zipped)
	_, _ = z.Write(make([]byte, bodyLimit+1))
	_ = z.Close()
	if _, _, err := patchBody(zipped.Bytes(), testSettings()); err == nil {
		t.Fatal("accepted oversized expansion")
	}
	if err := (Settings{Latitude: 91, Accuracy: 1}).Validate(); err == nil {
		t.Fatal("accepted invalid coordinate")
	}
}
func TestHTTPSProxyActuallyRewritesAndClearPassesThrough(t *testing.T) {
	authority, err := GenerateAuthorityJSON()
	if err != nil {
		t.Fatal(err)
	}
	ca, _ := parseAuthority(authority)
	key, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	leaf := &x509.Certificate{SerialNumber: big.NewInt(3), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), DNSNames: []string{"gs-loc.apple.com"}, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, KeyUsage: x509.KeyUsageDigitalSignature}
	der, err := x509.CreateCertificate(rand.Reader, leaf, ca.Leaf, &key.PublicKey, ca.PrivateKey)
	if err != nil {
		t.Fatal(err)
	}
	upstream := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(fixture()) }))
	upstream.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der, ca.Certificate[0]}, PrivateKey: key}}, MinVersion: tls.VersionTLS12}
	upstream.StartTLS()
	defer upstream.Close()
	dialer := net.Dialer{Timeout: time.Second}
	p, err := NewProxy(authority, settingsJSON(testSettings()), func(ctx context.Context, network, address string) (net.Conn, error) {
		return dialer.DialContext(ctx, network, upstream.Listener.Addr().String())
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = p.Close() }()
	roots := x509.NewCertPool()
	roots.AddCert(ca.Leaf)
	p.transport.TLSClientConfig = &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL(p.Port())), TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 5 * time.Second}
	request := func() []byte {
		resp, err := client.Post("https://gs-loc.apple.com/clls/wloc", "application/octet-stream", bytes.NewReader([]byte{1}))
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		b, _ := io.ReadAll(resp.Body)
		return b
	}
	if bytes.Equal(request(), fixture()) {
		t.Fatal("TLS proxy did not replace response")
	}
	disabled := testSettings()
	disabled.Enabled = false
	if err := p.Apply(settingsJSON(disabled)); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(request(), fixture()) {
		t.Fatal("disabled location still changes response")
	}
	var status Snapshot
	_ = json.Unmarshal([]byte(p.SnapshotJSON()), &status)
	if status.Enabled || status.Patched != 1 || status.Requests != 2 {
		t.Fatalf("incorrect evidence: %+v", status)
	}
	// Hijacked CONNECT streams must close on shutdown, not outlive the VPN.
	_ = p.Close()
	p.mu.Lock()
	clients := len(p.clients)
	p.mu.Unlock()
	if clients != 0 {
		t.Fatal("proxy retained hijacked connections")
	}
}
func TestAuthorityProfileAndTrustServer(t *testing.T) {
	raw, err := GenerateAuthorityJSON()
	if err != nil {
		t.Fatal(err)
	}
	second, _ := GenerateAuthorityJSON()
	if raw == second {
		t.Fatal("authority reused")
	}
	s, err := NewCertificateServer(raw)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	response, err := http.Get(s.DownloadURL())
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if !bytes.Contains(body, []byte("com.apple.security.root")) || bytes.Contains(body, []byte("PRIVATE KEY")) {
		t.Fatal("invalid/public profile contains key")
	}
	untrusted := &http.Client{Transport: &http.Transport{}, Timeout: 2 * time.Second}
	if r, err := untrusted.Get(s.ProbeURL()); err == nil {
		_ = r.Body.Close()
		t.Fatal("untrusted CA was accepted")
	}
	cert, _ := parseAuthority(raw)
	roots := x509.NewCertPool()
	roots.AddCert(cert.Leaf)
	trusted := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}, Timeout: 2 * time.Second}
	r, err := trusted.Get(s.ProbeURL())
	if err != nil {
		t.Fatal(err)
	}
	token, _ := io.ReadAll(r.Body)
	_ = r.Body.Close()
	if string(token) != s.Token() {
		t.Fatal("trust token mismatch")
	}
}
