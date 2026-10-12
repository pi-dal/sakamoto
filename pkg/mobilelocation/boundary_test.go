package mobilelocation

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestOrdinaryHTTPUsesOriginalLocalProxy(t *testing.T) {
	var requests atomic.Int32
	original := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host != "ordinary.example" {
			t.Errorf("lost original destination: %s", r.Host)
		}
		requests.Add(1)
		_, _ = io.WriteString(w, "original core route")
	}))
	defer original.Close()
	raw, _ := GenerateAuthorityJSON()
	p, err := NewProxy(raw, settingsJSON(testSettings()), func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("ordinary HTTP must reach the original core proxy directly")
	})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	if err := p.SetFallback(strings.TrimPrefix(original.URL, "http://")); err != nil {
		t.Fatal(err)
	}
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL(p.Port()))}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport}
	response, err := client.Get("http://ordinary.example/resource")
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if !bytes.Equal(body, []byte("original core route")) || requests.Load() != 1 {
		t.Fatalf("fallback failed: %s", body)
	}
	if err := p.SetFallback("192.0.2.1:80"); err == nil {
		t.Fatal("accepted a remote fallback")
	}
	if err := p.SetFallback("127.0.0.1:" + proxyURL(p.Port()).Port()); err == nil {
		t.Fatal("accepted a proxy loop")
	}
}

func TestCellularCoordinatesAndMissingAccuracy(t *testing.T) {
	point := []byte{8, 1, 16, 2} // coordinates with no accuracy and no unknown fields
	cell := encodeField(field{number: 5, kind: 2}, point)
	for _, number := range []int{22, 24} {
		payload := encodeField(field{number: number, kind: 2}, cell)
		patched, count, err := transformRecords(payload, testSettings())
		if err != nil || count != 1 {
			t.Fatalf("cell %d: %d %v", number, count, err)
		}
		top, _ := decodeMessage(patched)
		record, _ := decodeMessage(top[0].data)
		coordinates, _ := decodeMessage(record[0].data)
		if len(coordinates) != 3 || coordinates[2].number != 3 || !bytes.Equal(coordinates[2].data, []byte{15}) {
			t.Fatal("missing accuracy was not supplied")
		}
	}
	// A MAC-like sibling is required; arbitrary nested messages are immutable.
	payload := encodeField(field{number: 2, kind: 2}, cell)
	updated, count, err := transformRecords(payload, testSettings())
	if err != nil || count != 0 || !bytes.Equal(updated, payload) {
		t.Fatal("unidentified Wi-Fi record changed")
	}
}

func TestUpstreamBodyStallHasAWholeRequestDeadline(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(http.StatusOK)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	defer upstream.Close()
	raw, _ := GenerateAuthorityJSON()
	dialer := net.Dialer{Timeout: time.Second}
	p, err := NewProxy(raw, settingsJSON(testSettings()), func(ctx context.Context, network, address string) (net.Conn, error) {
		return dialer.DialContext(ctx, network, upstream.Listener.Addr().String())
	})
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	transport := &http.Transport{Proxy: http.ProxyURL(proxyURL(p.Port()))}
	defer transport.CloseIdleConnections()
	client := &http.Client{Transport: transport, Timeout: 15 * time.Second}
	started := time.Now()
	response, err := client.Post("http://gs-loc.apple.com/clls/wloc", "application/octet-stream", bytes.NewReader(nil))
	if err == nil {
		status := response.StatusCode
		_, err = io.ReadAll(response.Body)
		_ = response.Body.Close()
		if err == nil && status == http.StatusOK {
			t.Fatal("stalled upstream was reported as a successful response")
		}
	}
	if elapsed := time.Since(started); elapsed > 14*time.Second {
		t.Fatalf("request deadline did not bound body read: %v", elapsed)
	}
}
