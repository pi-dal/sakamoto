package icloud

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/pi-dal/sakamoto/internal/config"
)

type sourcePair struct{ name, local, remote string }

var inlineConfComment = regexp.MustCompile(`\s//`)
var ruleConfSection = regexp.MustCompile(`(?im)^[ \t]*\[(general|rule)\][ \t]*(//[^\r\n]*)?\r?$`)

// ValidSourceName allows source paths but rejects traversal, hidden components,
// generated state and credential filenames at any nesting depth.
func ValidSourceName(name string) bool {
	if name == "" || filepath.IsAbs(name) || strings.ContainsAny(name, "\\\x00") || filepath.ToSlash(filepath.Clean(name)) != name {
		return false
	}
	first := strings.ToLower(strings.Split(name, "/")[0])
	if first == "logs" {
		return false
	}
	for _, part := range strings.Split(name, "/") {
		if part == "" || part == "." || part == ".." || strings.HasPrefix(part, ".") {
			return false
		}
		lower := strings.ToLower(part)
		switch lower {
		case "s3-credentials.json", "s3-state.json", "config.json", "sakamoto.yaml", "proxy-restore.json", "auto-proxy.json", "api-rotation.pending.json", "watch.sock", "watch.lock", "svc.sock", "dns-restore.json", "icloud-state.json", "auth.json", "secrets.zsh":
			return false
		}
		for _, suffix := range []string{".srs", ".log", ".sock", ".lock", ".db", ".db.rule", ".pem", ".key"} {
			if strings.HasSuffix(lower, suffix) {
				return false
			}
		}
	}
	return true
}

// checkPath rejects symlinks instead of allowing a source or cloud dependency
// to read/overwrite files outside its declared directory.
func checkPath(root, path string) error {
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("source path escapes its root")
	}
	current := root
	parts := append([]string{""}, strings.Split(rel, string(filepath.Separator))...)
	for _, part := range parts {
		if part != "" && part != "." {
			current = filepath.Join(current, part)
		}
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing a symlink in source path")
		}
		if current != path && !info.IsDir() {
			return fmt.Errorf("source parent is not a directory")
		}
		if current == path && !info.Mode().IsRegular() {
			return fmt.Errorf("source is not a regular file")
		}
	}
	return nil
}

func sourcePairs(localDir string, cfg *config.Config) ([]sourcePair, error) {
	pairs := map[string]sourcePair{}
	locals := map[string]string{}
	add := func(name, local string) error {
		name = filepath.ToSlash(name)
		if !ValidSourceName(name) {
			return fmt.Errorf("sync of %q is forbidden (source paths only; never generated state or keys)", name)
		}
		local = filepath.Clean(local)
		if old, ok := locals[local]; ok && old != name {
			return fmt.Errorf("source has two conflicting cloud paths: %s and %s", old, name)
		}
		if old, ok := pairs[name]; ok && old.local != local {
			return fmt.Errorf("cloud path %s maps to different local sources", name)
		}
		pairs[name] = sourcePair{name, local, filepath.Join(cfg.ICloud.Directory, filepath.FromSlash(name))}
		locals[local] = name
		return nil
	}
	for _, name := range cfg.ICloud.Files {
		if err := add(name, filepath.Join(localDir, filepath.FromSlash(name))); err != nil {
			return nil, err
		}
	}
	if cfg.ICloud.IncludeConf && cfg.ConfPath != "" && !strings.Contains(cfg.ConfPath, "://") {
		main, err := filepath.Abs(cfg.ConfPath)
		if err != nil {
			return nil, err
		}
		if !strings.EqualFold(filepath.Ext(main), ".conf") {
			return nil, fmt.Errorf("automatic rule sync requires a local .conf source")
		}
		base := filepath.Dir(main)
		rel, err := filepath.Rel(localDir, main)
		if err != nil {
			return nil, err
		}
		if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			rel = filepath.Join("conf", filepath.Base(main))
		}
		cloudBase := filepath.Dir(rel)
		visiting, done := map[string]bool{}, map[string]bool{}
		var discover func(string, string, int) error
		discover = func(local, name string, depth int) error {
			if depth > 8 || len(done) > 128 {
				return fmt.Errorf("rule include graph exceeds sync limits")
			}
			if visiting[local] {
				return fmt.Errorf("rule include cycle detected")
			}
			if done[local] {
				return nil
			}
			if !strings.EqualFold(filepath.Ext(local), ".conf") {
				return fmt.Errorf("only .conf rule includes may be discovered automatically")
			}
			if err := add(name, local); err != nil {
				return err
			}
			pair := pairs[filepath.ToSlash(name)]
			if err := checkPath(base, local); err != nil {
				return err
			}
			if err := checkPath(cfg.ICloud.Directory, pair.remote); err != nil {
				return err
			}
			visiting[local] = true
			defer func() { visiting[local] = false }()
			found := false
			for _, path := range []string{pair.local, pair.remote} {
				data, exists, err := read(path)
				if err != nil {
					return err
				}
				if !exists {
					continue
				}
				found = true
				includes, err := localRuleIncludes(data)
				if err != nil {
					return err
				}
				for _, include := range includes {
					child := filepath.Join(filepath.Dir(local), filepath.FromSlash(include))
					childRel, err := filepath.Rel(base, child)
					if err != nil {
						return err
					}
					if err := discover(child, filepath.Join(cloudBase, childRel), depth+1); err != nil {
						return err
					}
				}
			}
			if !found && depth > 0 {
				return fmt.Errorf("required conf include is missing on both sides")
			}
			done[local] = true
			return nil
		}
		if err := discover(main, rel, 0); err != nil {
			return nil, err
		}
	}
	result := make([]sourcePair, 0, len(pairs))
	for _, pair := range pairs {
		// Automatic external conf is confined to its own parent; explicit
		// paths always stay inside the runtime directory.
		base := localDir
		if rel, _ := filepath.Rel(localDir, pair.local); rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			absolute, err := filepath.Abs(cfg.ConfPath)
			if err != nil {
				return nil, err
			}
			base = filepath.Dir(absolute)
		}
		if err := checkPath(base, pair.local); err != nil {
			return nil, err
		}
		if err := checkPath(cfg.ICloud.Directory, pair.remote); err != nil {
			return nil, err
		}
		result = append(result, pair)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].name < result[j].name })
	return result, nil
}

// localRuleIncludes parses only local relative includes in [General]. Remote
// URLs are fetched by the importer, not treated as cloud file dependencies.
func localRuleIncludes(data []byte) ([]string, error) {
	if len(data) > 16<<20 {
		return nil, fmt.Errorf("conf source exceeds the 16 MiB sync limit")
	}
	text := strings.TrimPrefix(string(data), "\ufeff")
	if !ruleConfSection.MatchString(text) {
		return nil, fmt.Errorf("automatic conf source lacks [General] or [Rule]")
	}
	var includes []string
	section := ""
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(inlineConfComment.Split(line, 2)[0])
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			section = strings.ToLower(line)
			continue
		}
		if section != "[general]" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || !strings.EqualFold(strings.TrimSpace(key), "include") {
			continue
		}
		for _, include := range strings.Split(value, ",") {
			include = strings.TrimSpace(include)
			if include == "" || strings.HasPrefix(include, "https://") || strings.HasPrefix(include, "http://") {
				continue
			}
			if !ValidSourceName("conf/" + include) {
				return nil, fmt.Errorf("unsafe relative conf include")
			}
			includes = append(includes, include)
		}
	}
	return includes, nil
}
