// Package restorepoint retains complete, private runtime bundles. It has no
// native-core dependency and never initiates a network connection.
package restorepoint

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/pi-dal/sakamoto/internal/recovery"
)

const liveRules = "auto-proxy-rules.json"
const maxInput = 64 << 20

type input struct {
	Path string
	Data []byte
	Live bool
}
type Candidate struct {
	dir                   string
	raw, sidecar, learned []byte
	inputs                []input
	engine                *os.File
	engineInfo            os.FileInfo
	closed                bool
}
type Manifest struct {
	Schema   int               `json:"schema"`
	Runtime  string            `json:"runtime"`
	PID      int               `json:"pid"`
	Epoch    time.Time         `json:"core_epoch"`
	Verified time.Time         `json:"verified"`
	Files    map[string]string `json:"sha256"`
}
type Point struct {
	Dir      string
	Manifest Manifest
}

func read(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Size() > maxInput {
		return nil, errors.New("invalid or oversized runtime asset")
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxInput+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxInput {
		return nil, errors.New("runtime asset exceeded limit")
	}
	return raw, nil
}
func Capture(dir, binary string) (*Candidate, error) {
	c := &Candidate{dir: dir}
	var err error
	if c.raw, err = read(filepath.Join(dir, "config.json")); err != nil {
		return nil, err
	}
	if c.sidecar, err = read(filepath.Join(dir, "sakamoto.yaml")); err != nil {
		return nil, err
	}
	c.learned, err = read(filepath.Join(dir, "auto-proxy.json"))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	var config any
	if err = json.Unmarshal(c.raw, &config); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	total := 0
	err = walkPaths(config, func(path string) (string, error) {
		absolute := path
		if !filepath.IsAbs(absolute) {
			absolute = filepath.Join(dir, path)
		}
		absolute = filepath.Clean(absolute)
		if !seen[absolute] {
			raw, e := read(absolute)
			if e != nil {
				return "", fmt.Errorf("missing runtime asset %s: %w", filepath.Base(absolute), e)
			}
			total += len(raw)
			if total > 128<<20 {
				return "", errors.New("runtime assets exceeded total limit")
			}
			c.inputs = append(c.inputs, input{absolute, raw, absolute == filepath.Join(dir, liveRules)})
			seen[absolute] = true
		}
		return path, nil
	})
	if err != nil {
		return nil, err
	}
	c.engine, err = os.Open(binary)
	if err != nil {
		return nil, err
	}
	c.engineInfo, err = c.engine.Stat()
	if err != nil {
		c.Close()
		return nil, err
	}
	if !c.engineInfo.Mode().IsRegular() || c.engineInfo.Size() > 512<<20 {
		c.Close()
		return nil, errors.New("invalid native executable")
	}
	return c, nil
}
func (c *Candidate) Fingerprint() string {
	hash := sha256.New()
	hash.Write(c.raw)
	hash.Write([]byte{0})
	hash.Write(c.sidecar)
	hash.Write([]byte{0})
	return hex.EncodeToString(hash.Sum(nil))
}
func (c *Candidate) Close() {
	if !c.closed {
		c.closed = true
		if c.engine != nil {
			_ = c.engine.Close()
		}
	}
}
func (c *Candidate) Unchanged() bool {
	for _, item := range append([]input{{Path: filepath.Join(c.dir, "config.json"), Data: c.raw}, {Path: filepath.Join(c.dir, "sakamoto.yaml"), Data: c.sidecar}}, c.inputs...) {
		raw, err := read(item.Path)
		if err != nil || !bytes.Equal(raw, item.Data) {
			return false
		}
	}
	learned, learnedErr := read(filepath.Join(c.dir, "auto-proxy.json"))
	if c.learned == nil {
		if !errors.Is(learnedErr, os.ErrNotExist) {
			return false
		}
	} else if learnedErr != nil || !bytes.Equal(learned, c.learned) {
		return false
	}
	info, err := c.engine.Stat()
	return err == nil && info.Size() == c.engineInfo.Size() && info.ModTime().Equal(c.engineInfo.ModTime())
}
func digest(raw []byte) string { sum := sha256.Sum256(raw); return hex.EncodeToString(sum[:]) }
func privateDir(path string) error {
	if err := os.Mkdir(path, 0700); err != nil && !errors.Is(err, os.ErrExist) {
		return err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return errors.New("unsafe recovery directory")
	}
	if os.Geteuid() == 0 {
		if st, ok := info.Sys().(*syscall.Stat_t); !ok || st.Uid != 0 {
			return errors.New("recovery directory is not root-owned")
		}
	}
	return nil
}
func (c *Candidate) Promote(pid int, epoch time.Time) (*Point, error) {
	if !c.Unchanged() {
		return nil, errors.New("runtime inputs changed; stable receipt was not promoted")
	}
	base := filepath.Join(c.dir, "recovery")
	if err := privateDir(base); err != nil {
		return nil, err
	}
	bundles := filepath.Join(base, "bundles")
	if err := privateDir(bundles); err != nil {
		return nil, err
	}
	folder, err := os.MkdirTemp(bundles, "runtime-")
	if err != nil {
		return nil, err
	}
	keep := false
	defer func() {
		if !keep {
			_ = os.RemoveAll(folder)
		}
	}()
	manifest := Manifest{Schema: 1, Runtime: c.dir, PID: pid, Epoch: epoch, Verified: time.Now().UTC(), Files: map[string]string{}}
	put := func(name string, raw []byte) error {
		manifest.Files[name] = digest(raw)
		return recovery.Atomic(filepath.Join(folder, name), raw, 0600)
	}
	paths := map[string]string{}
	for i, item := range c.inputs {
		name := fmt.Sprintf("asset-%d%s", i, filepath.Ext(item.Path))
		if err = put(name, item.Data); err != nil {
			return nil, err
		}
		if item.Live {
			paths[item.Path] = filepath.Join(c.dir, liveRules)
			if err = put(liveRules, item.Data); err != nil {
				return nil, err
			}
		} else {
			paths[item.Path] = filepath.Join(folder, name)
		}
	}
	var config any
	if err := json.Unmarshal(c.raw, &config); err != nil {
		return nil, err
	}
	if err = walkPaths(config, func(path string) (string, error) {
		absolute := path
		if !filepath.IsAbs(path) {
			absolute = filepath.Join(c.dir, path)
		}
		replacement, ok := paths[filepath.Clean(absolute)]
		if !ok {
			return "", errors.New("uncaptured runtime path")
		}
		return replacement, nil
	}); err != nil {
		return nil, err
	}
	raw, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return nil, err
	}
	if err = put("config.json", raw); err != nil {
		return nil, err
	}
	if err = put("sakamoto.yaml", c.sidecar); err != nil {
		return nil, err
	}
	if c.learned != nil {
		if err = put("auto-proxy.json", c.learned); err != nil {
			return nil, err
		}
	}
	engine, err := os.OpenFile(filepath.Join(folder, "engine"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0700)
	if err != nil {
		return nil, err
	}
	if _, err = c.engine.Seek(0, io.SeekStart); err != nil {
		_ = engine.Close()
		return nil, err
	}
	hash := sha256.New()
	_, copyErr := io.Copy(io.MultiWriter(engine, hash), io.LimitReader(c.engine, 512<<20+1))
	syncErr := engine.Sync()
	closeErr := engine.Close()
	if copyErr != nil || syncErr != nil || closeErr != nil {
		return nil, errors.Join(copyErr, syncErr, closeErr)
	}
	if !c.Unchanged() {
		return nil, errors.New("runtime changed during promotion")
	}
	manifest.Files["engine"] = hex.EncodeToString(hash.Sum(nil))
	raw, err = json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return nil, err
	}
	if err = recovery.Atomic(filepath.Join(folder, "receipt.json"), raw, 0600); err != nil {
		return nil, err
	}
	pointer, _ := json.Marshal(map[string]string{"bundle": filepath.Base(folder), "sha256": digest(raw)})
	if err = recovery.Atomic(filepath.Join(base, "last-good.json"), pointer, 0600); err != nil {
		return nil, err
	}
	keep = true
	return &Point{folder, manifest}, nil
}
func Load(dir string) (*Point, error) {
	base := filepath.Join(dir, "recovery")
	if err := privateDir(base); err != nil {
		return nil, err
	}
	if err := privateDir(filepath.Join(base, "bundles")); err != nil {
		return nil, err
	}
	raw, err := recovery.ReadPrivate(filepath.Join(base, "last-good.json"), 4096)
	if err != nil {
		return nil, err
	}
	var pointer struct{ Bundle, SHA256 string }
	if err = json.Unmarshal(raw, &pointer); err != nil {
		return nil, err
	}
	if !strings.HasPrefix(pointer.Bundle, "runtime-") || filepath.Base(pointer.Bundle) != pointer.Bundle {
		return nil, errors.New("invalid recovery pointer")
	}
	folder := filepath.Join(base, "bundles", pointer.Bundle)
	if err = privateDir(folder); err != nil {
		return nil, err
	}
	raw, err = recovery.ReadPrivate(filepath.Join(folder, "receipt.json"), 1<<20)
	if err != nil {
		return nil, err
	}
	if digest(raw) != pointer.SHA256 {
		return nil, errors.New("recovery manifest checksum mismatch")
	}
	p := &Point{Dir: folder}
	if err = json.Unmarshal(raw, &p.Manifest); err != nil {
		return nil, err
	}
	if p.Manifest.Schema != 1 || p.Manifest.Runtime != dir {
		return nil, errors.New("recovery bundle belongs to another runtime")
	}
	for name, expected := range p.Manifest.Files {
		if filepath.Base(name) != name || name == "." || name == ".." {
			return nil, errors.New("unsafe recovery asset name")
		}
		path := filepath.Join(folder, name)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 {
			return nil, errors.New("unsafe recovery asset")
		}
		if os.Geteuid() == 0 {
			if st, ok := info.Sys().(*syscall.Stat_t); !ok || st.Uid != 0 {
				return nil, errors.New("recovery asset is not root-owned")
			}
		}
		file, err := os.Open(path)
		if err != nil {
			return nil, err
		}
		hash := sha256.New()
		_, err = io.Copy(hash, io.LimitReader(file, 512<<20+1))
		_ = file.Close()
		if err != nil || hex.EncodeToString(hash.Sum(nil)) != expected {
			return nil, errors.New("recovery asset checksum mismatch")
		}
	}
	for _, name := range []string{"config.json", "sakamoto.yaml", "engine"} {
		if p.Manifest.Files[name] == "" {
			return nil, errors.New("incomplete recovery bundle")
		}
	}
	return p, nil
}
func (p *Point) Apply(dir string) error { return p.ApplyChecked(dir, nil) }

// ApplyChecked checks the caller's preparation receipt inside the same short
// transaction that fences learning writers and replaces the runtime files.
func (p *Point) ApplyChecked(dir string, check func() error) error {
	unlock, err := recovery.LockRules(dir)
	if err != nil {
		return err
	}
	defer unlock()
	if check != nil {
		if err := check(); err != nil {
			return err
		}
	}
	if _, err = recovery.AdvanceRulesGeneration(dir); err != nil {
		return err
	}
	for _, name := range []string{liveRules, "auto-proxy.json", "sakamoto.yaml", "config.json"} {
		if p.Manifest.Files[name] == "" {
			if name == liveRules || name == "auto-proxy.json" {
				if err := os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
					return err
				}
			}
			continue
		}
		raw, err := read(filepath.Join(p.Dir, name))
		if err != nil {
			return err
		}
		if err = recovery.AtomicUser(filepath.Join(dir, name), raw, 0600); err != nil {
			return err
		}
	}
	return nil
}

// Only immutable input files are captured. Mutable cache/state directories and
// protocol strings named "path" (for example a WebSocket URL) are not files.
func walkPaths(value any, replace func(string) (string, error)) error {
	switch node := value.(type) {
	case map[string]any:
		for key, child := range node {
			file := key == "certificate_path" || key == "key_path" || (key == "path" && (node["type"] == "local" || (node["type"] == nil && node["tag"] != nil && node["format"] != nil)))
			if path, ok := child.(string); file && ok && path != "" {
				next, err := replace(path)
				if err != nil {
					return err
				}
				node[key] = next
			} else if err := walkPaths(child, replace); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range node {
			if err := walkPaths(child, replace); err != nil {
				return err
			}
		}
	}
	return nil
}
