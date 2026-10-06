// Package sourcesync exchanges validated source bundles across S3-compatible
// stores. It never carries generated configurations or device credentials.
package sourcesync

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/pi-dal/sakamoto/pkg/mobileconf"
)

const MaxBytes = 32 << 20

type Bundle struct {
	Version  int               `json:"version"`
	MainConf string            `json:"main_conf,omitempty"`
	Files    map[string]string `json:"files"`
}
type Policy struct {
	Match  string `json:"match"`
	Action string `json:"action"`
}
type Subscription struct {
	Name   string `json:"name"`
	URL    string `json:"url"`
	Format string `json:"format"`
}
type Baseline struct {
	Target   string            `json:"target"`
	Hashes   map[string]string `json:"hashes"`
	MainConf string            `json:"main_conf,omitempty"`
}

func ValidName(name string) bool {
	if name == "nodes.txt" || name == "policy.json" || name == "subscriptions.json" {
		return true
	}
	if !strings.HasPrefix(name, "conf/") || !strings.HasSuffix(name, ".conf") || path.Clean(name) != name || strings.ContainsAny(name, "\\\x00\r\n") {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || strings.HasPrefix(part, ".") {
			return false
		}
	}
	return true
}

// Canonical folds structured source JSON into a common representation so Swift,
// Kotlin and Go key ordering cannot fabricate edits or conflicts.
func Canonical(b Bundle) Bundle {
	files := make(map[string]string, len(b.Files))
	for name, content := range b.Files {
		switch name {
		case "policy.json":
			var rows []Policy
			if json.Unmarshal([]byte(content), &rows) == nil {
				if rows == nil {
					rows = []Policy{}
				}
				raw, _ := json.Marshal(rows)
				content = string(raw)
			}
		case "subscriptions.json":
			var rows []Subscription
			if json.Unmarshal([]byte(content), &rows) == nil {
				if rows == nil {
					rows = []Subscription{}
				}
				raw, _ := json.Marshal(rows)
				content = string(raw)
			}
		}
		files[name] = content
	}
	b.Files = files
	return b
}
func Decode(raw string) (Bundle, error) {
	if len(raw) > MaxBytes {
		return Bundle{}, errors.New("source bundle exceeds 32 MiB")
	}
	var b Bundle
	if err := json.Unmarshal([]byte(raw), &b); err != nil {
		return b, errors.New("invalid source bundle JSON")
	}
	if b.Files == nil {
		b.Files = map[string]string{}
	}
	if err := b.Validate(); err != nil {
		return b, err
	}
	return Canonical(b), nil
}
func (b Bundle) Validate() error {
	if b.Version != 1 || len(b.Files) > 128 {
		return errors.New("unsupported source bundle version or too many files")
	}
	size := 0
	for name, content := range b.Files {
		size += len(content)
		if !ValidName(name) || size > MaxBytes {
			return errors.New("invalid source name or bundle size")
		}
		switch name {
		case "nodes.txt":
			for _, line := range strings.Split(content, "\n") {
				line = strings.TrimSpace(line)
				if line == "" || strings.HasPrefix(line, "#") {
					continue
				}
				nodes, _ := mobileconf.ParseShareLines([]string{line})
				if len(nodes) != 1 {
					return errors.New("invalid node source in bundle")
				}
			}
		case "policy.json":
			var rules []Policy
			if err := json.Unmarshal([]byte(content), &rules); err != nil {
				return errors.New("invalid policy source")
			}
			for _, r := range rules {
				if _, _, _, err := mobileconf.NormalizePolicyRule(r.Match, r.Action); err != nil {
					return errors.New("invalid policy rule in bundle")
				}
			}
		case "subscriptions.json":
			var subs []Subscription
			if err := json.Unmarshal([]byte(content), &subs); err != nil {
				return errors.New("invalid subscription source")
			}
			for _, s := range subs {
				if strings.TrimSpace(s.Name) == "" || !mobileconf.IsRemote(s.URL) || mobileconf.ValidSource(s.URL) != nil {
					return errors.New("invalid subscription source")
				}
				switch s.Format {
				case "", "auto", "singbox", "clash", "base64":
				default:
					return errors.New("invalid subscription format")
				}
			}
		default:
			if _, err := mobileconf.ParseDocument(content, mobileconf.Hooks{}); err != nil {
				return fmt.Errorf("invalid conf source: %s", name)
			}
		}
	}
	if b.MainConf != "" {
		if !strings.HasPrefix(b.MainConf, "conf/") || !ValidName(b.MainConf) {
			return errors.New("invalid main conf path")
		}
		visiting, done := map[string]bool{}, map[string]bool{}
		var visit func(string, int) error
		visit = func(name string, depth int) error {
			if depth > 8 || visiting[name] {
				return errors.New("conf include cycle or depth limit")
			}
			if done[name] {
				return nil
			}
			content, ok := b.Files[name]
			if !ok {
				return errors.New("conf include missing from source bundle")
			}
			doc, err := mobileconf.ParseDocument(content, mobileconf.Hooks{})
			if err != nil {
				return errors.New("invalid conf include")
			}
			visiting[name] = true
			for _, inc := range doc.Includes {
				inc = strings.TrimSpace(inc)
				if inc == "" || mobileconf.IsRemote(inc) {
					continue
				}
				child := path.Join(path.Dir(name), inc)
				if !ValidName(child) || path.IsAbs(inc) || strings.Contains(inc, "..") {
					return errors.New("unsafe conf include")
				}
				if err := visit(child, depth+1); err != nil {
					return err
				}
			}
			delete(visiting, name)
			done[name] = true
			return nil
		}
		if err := visit(b.MainConf, 0); err != nil {
			return err
		}
	}
	return nil
}
func Hash(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func baseline(b Bundle, target string) Baseline {
	result := Baseline{Target: target, Hashes: map[string]string{}, MainConf: b.MainConf}
	for name, value := range b.Files {
		result.Hashes[name] = Hash(value)
	}
	return result
}

// Merge is a three-way merge per source. Unknown differing copies and deletions
// are conflicts; no timestamps or device clocks decide which copy wins.
func Merge(local, remote Bundle, base Baseline) (Bundle, []string, error) {
	merged := Bundle{Version: 1, Files: map[string]string{}}
	names := map[string]bool{}
	for n := range local.Files {
		names[n] = true
	}
	for n := range remote.Files {
		names[n] = true
	}
	for n := range base.Hashes {
		names[n] = true
	}
	var downloads []string
	ordered := make([]string, 0, len(names))
	for n := range names {
		ordered = append(ordered, n)
	}
	sort.Strings(ordered)
	for _, name := range ordered {
		l, le := local.Files[name]
		r, re := remote.Files[name]
		prev, known := base.Hashes[name]
		switch {
		case le && re && l == r:
			merged.Files[name] = l
		case !le && !re:
			if known {
				return Bundle{}, nil, fmt.Errorf("sync conflict: source removed: %s", name)
			}
		case !le && re:
			if known {
				return Bundle{}, nil, fmt.Errorf("sync conflict: local source removed: %s", name)
			}
			merged.Files[name] = r
			downloads = append(downloads, name)
		case le && !re:
			if known {
				return Bundle{}, nil, fmt.Errorf("sync conflict: remote source removed: %s", name)
			}
			merged.Files[name] = l
		case known && Hash(l) == prev:
			merged.Files[name] = r
			downloads = append(downloads, name)
		case known && Hash(r) == prev:
			merged.Files[name] = l
		default:
			return Bundle{}, nil, fmt.Errorf("sync conflict: both copies changed: %s", name)
		}
	}
	switch {
	case local.MainConf == remote.MainConf:
		merged.MainConf = local.MainConf
	case local.MainConf == "" && base.MainConf == "":
		merged.MainConf = remote.MainConf
	case remote.MainConf == "" && base.MainConf == "":
		merged.MainConf = local.MainConf
	case base.MainConf == local.MainConf:
		merged.MainConf = remote.MainConf
	case base.MainConf == remote.MainConf:
		merged.MainConf = local.MainConf
	default:
		return Bundle{}, nil, errors.New("sync conflict: main conf changed on both devices")
	}
	if err := merged.Validate(); err != nil {
		return Bundle{}, nil, err
	}
	return merged, downloads, nil
}
