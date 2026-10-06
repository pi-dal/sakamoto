package sourcesync

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestThreeWayConflictAndIncludes(t *testing.T) {
	a := Bundle{Version: 1, Files: map[string]string{"policy.json": `[{"match":"example.com","action":"proxy"}]`}}
	b := Bundle{Version: 1, Files: map[string]string{"policy.json": `[{"match":"example.com","action":"direct"}]`}}
	if _, _, err := Merge(a, b, Baseline{}); err == nil {
		t.Fatal("unknown divergent sources overwritten")
	}
	merged, download, err := Merge(a, b, baseline(a, "test"))
	if err != nil || merged.Files["policy.json"] != b.Files["policy.json"] || len(download) != 1 {
		t.Fatal("remote-only edit not adopted", err)
	}
	b.Files = map[string]string{}
	if _, _, err := Merge(a, b, baseline(a, "test")); err == nil {
		t.Fatal("remote deletion overwritten")
	}
	for _, files := range []map[string]string{{"config.json": "{}"}, {"conf/main.conf": "[General]\ninclude=missing.conf\n[Rule]\n"}, {"conf/main.conf": "[General]\ninclude=../secret.conf\n[Rule]\n"}} {
		bundle := Bundle{Version: 1, MainConf: "conf/main.conf", Files: files}
		if bundle.Validate() == nil {
			t.Fatal("unsafe/incomplete bundle accepted")
		}
	}
}
func TestS3RoundTripAndConditionalWrite(t *testing.T) {
	var stored []byte
	tls := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bucket/sakamoto/sources-v1.json" {
			t.Error("wrong object path", r.URL.Path)
		}
		if !strings.Contains(r.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=example/") || r.Header.Get("X-Amz-Date") == "" {
			t.Error("missing SigV4")
		}
		switch r.Method {
		case "GET":
			if stored == nil {
				w.WriteHeader(404)
				return
			}
			w.Header().Set("ETag", `"one"`)
			_, _ = w.Write(stored)
		case "PUT":
			if stored == nil && r.Header.Get("If-None-Match") != "*" {
				t.Error("create not conditional")
			}
			if stored != nil && r.Header.Get("If-Match") != `"one"` {
				t.Error("update not conditional")
			}
			var b Bundle
			_ = json.NewDecoder(r.Body).Decode(&b)
			stored, _ = json.Marshal(b)
			w.Header().Set("ETag", `"one"`)
		}
	}))
	defer tls.Close()
	s := Settings{Enabled: true, Endpoint: tls.URL, Region: "auto", Bucket: "bucket", Prefix: "sakamoto"}
	c := Credentials{AccessKey: "example", SecretKey: "synthetic-secret"}
	b := Bundle{Version: 1, Files: map[string]string{"policy.json": "[]"}}
	result, err := Sync(context.Background(), s, c, b, Baseline{}, tls.Client())
	if err != nil || !result.Uploaded {
		t.Fatal("upload failed", err)
	}
	b.Files["policy.json"] = `[{"match":"example.com","action":"proxy"}]`
	result, err = Sync(context.Background(), s, c, b, result.Baseline, tls.Client())
	if err != nil || !result.Uploaded {
		t.Fatal("update failed", err)
	}
	empty := Bundle{Version: 1, Files: map[string]string{}}
	result, err = Sync(context.Background(), s, c, empty, Baseline{}, tls.Client())
	if err != nil || len(result.Downloads) != 1 {
		t.Fatal("fresh download failed", err)
	}
}
func TestSigV4KnownRequest(t *testing.T) {
	req, _ := http.NewRequest("GET", "https://examplebucket.s3.amazonaws.com/test.txt", nil)
	sign(req, nil, "us-east-1", Credentials{AccessKey: "AKIDEXAMPLE", SecretKey: "example"}, time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC))
	if !strings.HasSuffix(req.Header.Get("Authorization"), "Signature=6825c674b7c0b3afa8b6b9593ccfdcfdeb314c9db9a289d2d60aa347221b4476") {
		t.Fatal("SigV4 known request signature mismatch")
	}
	if awsPath("/a b/\u4f60\u597d+@") != "/a%20b/%E4%BD%A0%E5%A5%BD%2B%40" {
		t.Fatal("S3 key not RFC3986 encoded")
	}
	if req.Header.Get("X-Amz-Date") != "20130524T000000Z" || !strings.Contains(req.Header.Get("Authorization"), "20130524/us-east-1/s3/aws4_request") {
		t.Fatal("incorrect scope")
	}
}
func TestConditionalFailureLeavesNoPlan(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "GET" {
			w.Header().Set("ETag", `"one"`)
			_, _ = w.Write([]byte(`{"version":1,"files":{}}`))
		} else {
			w.WriteHeader(412)
		}
	}))
	defer server.Close()
	_, err := Sync(context.Background(), Settings{Enabled: true, Endpoint: server.URL, Region: "auto", Bucket: "b"}, Credentials{AccessKey: "x", SecretKey: "y"}, Bundle{Version: 1, Files: map[string]string{"policy.json": "[]"}}, Baseline{}, server.Client())
	if err == nil || !strings.Contains(err.Error(), "conflict") {
		t.Fatal("concurrent edit accepted", err)
	}
}
