package restorepoint

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pi-dal/sakamoto/internal/recovery"
)

type mobileManifest struct {
	Schema int               `json:"schema"`
	Files  map[string]string `json:"files"`
	Inputs map[string]string `json:"inputs"`
}
type MobileCandidate struct {
	root, folder string
	manifest     mobileManifest
	promoted     bool
}

func mobileBase(root string) string { return filepath.Join(root, "recovery", "mobile-profiles") }
func hashFile(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Size() > maxInput {
		return "", errors.New("invalid retained input")
	}
	hash := sha256.New()
	n, err := io.Copy(hash, io.LimitReader(f, maxInput+1))
	if err != nil {
		return "", err
	}
	if n != info.Size() {
		return "", errors.New("retained input changed while reading")
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

// CaptureMobile retains config/assets on disk before startup, without copying
// or loading executable code. Relative paths use the native working directory.
func CaptureMobile(root, content, metadata string, working ...string) (*MobileCandidate, error) {
	var config any
	if err := json.Unmarshal([]byte(content), &config); err != nil {
		return nil, errors.New("invalid retained profile")
	}
	if !json.Valid([]byte(metadata)) || len(content)+len(metadata) > maxInput {
		return nil, errors.New("invalid retained profile metadata")
	}
	base := mobileBase(root)
	if err := os.MkdirAll(filepath.Dir(base), 0700); err != nil {
		return nil, err
	}
	if err := privateDir(base); err != nil {
		return nil, err
	}
	folder, err := os.MkdirTemp(base, "profile-")
	if err != nil {
		return nil, err
	}
	c := &MobileCandidate{root: root, folder: folder, manifest: mobileManifest{Schema: 1, Files: map[string]string{}, Inputs: map[string]string{}}}
	keep := false
	defer func() {
		if !keep {
			c.Close()
		}
	}()
	sourceDir := root
	if len(working) > 0 && working[0] != "" {
		sourceDir = working[0]
	}
	total := int64(len(content) + len(metadata))
	paths := map[string]string{}
	err = walkPaths(config, func(path string) (string, error) {
		absolute := path
		if !filepath.IsAbs(absolute) {
			absolute = filepath.Join(sourceDir, path)
		}
		absolute = filepath.Clean(absolute)
		if replacement, ok := paths[absolute]; ok {
			return replacement, nil
		}
		source, err := os.Open(absolute)
		if err != nil {
			return "", errors.New("retained profile input is missing")
		}
		defer func() { _ = source.Close() }()
		info, err := source.Stat()
		if err != nil {
			return "", err
		}
		if !info.Mode().IsRegular() || info.Size() > maxInput {
			return "", errors.New("invalid retained profile input")
		}
		total += info.Size()
		if total > 128<<20 {
			return "", errors.New("retained profile exceeds size limit")
		}
		name := fmt.Sprintf("asset-%d", len(paths))
		out, err := os.OpenFile(filepath.Join(folder, name), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return "", err
		}
		hash := sha256.New()
		n, copyErr := io.Copy(io.MultiWriter(out, hash), io.LimitReader(source, maxInput+1))
		syncErr := out.Sync()
		closeErr := out.Close()
		if copyErr != nil || syncErr != nil || closeErr != nil || n != info.Size() {
			return "", errors.New("retained profile input changed while copying")
		}
		sum := hex.EncodeToString(hash.Sum(nil))
		c.manifest.Files[name] = sum
		c.manifest.Inputs[absolute] = sum
		replacement := filepath.Join(folder, name)
		paths[absolute] = replacement
		return replacement, nil
	})
	if err != nil {
		return nil, err
	}
	raw, err := json.Marshal(config)
	if err != nil {
		return nil, err
	}
	for name, data := range map[string][]byte{"config.json": raw, "metadata.json": []byte(metadata)} {
		if err = recovery.Atomic(filepath.Join(folder, name), data, 0600); err != nil {
			return nil, err
		}
		c.manifest.Files[name] = digest(data)
	}
	manifest, err := json.Marshal(c.manifest)
	if err != nil {
		return nil, err
	}
	if err = recovery.Atomic(filepath.Join(folder, "receipt.json"), manifest, 0600); err != nil {
		return nil, err
	}
	keep = true
	return c, nil
}
func (c *MobileCandidate) Content() (string, error) {
	content, _, err := loadMobileFolder(c.root, c.folder)
	return content, err
}

// RetainInputs preserves assets referenced by a succeeding running generation.
// Stable promotion later reclaims unreferenced private generations.
func (c *MobileCandidate) RetainInputs() { c.promoted = true }
func (c *MobileCandidate) Close() {
	if !c.promoted {
		_ = os.RemoveAll(c.folder)
	}
}
func (c *MobileCandidate) Promote() error {
	for path, want := range c.manifest.Inputs {
		got, err := hashFile(path)
		if err != nil || got != want {
			return errors.New("retained profile inputs changed before stable verification")
		}
	}
	if _, _, err := loadMobileFolder(c.root, c.folder); err != nil {
		return err
	}
	pointer, _ := json.Marshal(filepath.Base(c.folder))
	if err := recovery.Atomic(filepath.Join(mobileBase(c.root), "last-good.json"), pointer, 0600); err != nil {
		return err
	}
	c.promoted = true
	// Keep two stable generations and every directory referenced by the
	// currently applied content. A fallback can still own those input paths.
	entries, _ := os.ReadDir(mobileBase(c.root))
	protected := map[string]bool{filepath.Base(c.folder): true}
	for path := range c.manifest.Inputs {
		parent := filepath.Dir(path)
		if filepath.Dir(parent) == mobileBase(c.root) {
			protected[filepath.Base(parent)] = true
		}
	}
	type oldProfile struct {
		name     string
		modified int64
	}
	var old []oldProfile
	for _, entry := range entries {
		if entry.IsDir() && strings.HasPrefix(entry.Name(), "profile-") && !protected[entry.Name()] {
			if info, err := entry.Info(); err == nil {
				old = append(old, oldProfile{entry.Name(), info.ModTime().UnixNano()})
			}
		}
	}
	sort.Slice(old, func(i, j int) bool { return old[i].modified > old[j].modified })
	for i, entry := range old {
		if i > 0 {
			_ = os.RemoveAll(filepath.Join(mobileBase(c.root), entry.name))
		}
	}
	return nil
}
func LoadMobile(root string) (content, metadata string, err error) {
	raw, err := recovery.ReadPrivate(filepath.Join(mobileBase(root), "last-good.json"), 1024)
	if err != nil {
		return "", "", errors.New("no verified retained profile")
	}
	var name string
	if json.Unmarshal(raw, &name) != nil || filepath.Base(name) != name || !strings.HasPrefix(name, "profile-") {
		return "", "", errors.New("invalid retained profile pointer")
	}
	return loadMobileFolder(root, filepath.Join(mobileBase(root), name))
}
func loadMobileFolder(root, folder string) (string, string, error) {
	base := mobileBase(root)
	if filepath.Dir(folder) != base {
		return "", "", errors.New("retained profile escaped its directory")
	}
	info, err := os.Lstat(folder)
	if err != nil || !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return "", "", errors.New("unsafe retained profile directory")
	}
	raw, err := recovery.ReadPrivate(filepath.Join(folder, "receipt.json"), 1<<20)
	if err != nil {
		return "", "", err
	}
	var manifest mobileManifest
	if json.Unmarshal(raw, &manifest) != nil || manifest.Schema != 1 || len(manifest.Files) > 4096 || manifest.Files["config.json"] == "" || manifest.Files["metadata.json"] == "" {
		return "", "", errors.New("invalid retained profile manifest")
	}
	for name, want := range manifest.Files {
		if filepath.Base(name) != name {
			return "", "", errors.New("invalid retained profile asset")
		}
		got, err := hashFile(filepath.Join(folder, name))
		if err != nil || got != want {
			return "", "", errors.New("retained profile checksum mismatch")
		}
	}
	config, err := recovery.ReadPrivate(filepath.Join(folder, "config.json"), maxInput)
	if err != nil {
		return "", "", err
	}
	metadata, err := recovery.ReadPrivate(filepath.Join(folder, "metadata.json"), maxInput)
	if err != nil {
		return "", "", err
	}
	var decoded any
	if json.Unmarshal(config, &decoded) != nil || !json.Valid(metadata) {
		return "", "", errors.New("corrupt retained profile")
	}
	err = walkPaths(decoded, func(path string) (string, error) {
		if filepath.Dir(path) != folder || manifest.Files[filepath.Base(path)] == "" {
			return "", errors.New("unretained profile input")
		}
		return path, nil
	})
	if err != nil {
		return "", "", err
	}
	return string(config), string(metadata), nil
}
