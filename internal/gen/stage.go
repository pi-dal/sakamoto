package gen

import (
	"bytes"
	"encoding/json"
	"path/filepath"
)

// RebaseConfiguration retains checked compiled rules in their generation,
// resolves relative TLS inputs against the source runtime, and places the one
// mutable learned-rule file at its intended check/activation location.
func RebaseConfiguration(raw []byte, rulesDir, sourceDir, liveDir string) ([]byte, error) {
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&value); err != nil {
		return nil, err
	}
	var walk func(any)
	walk = func(value any) {
		switch node := value.(type) {
		case map[string]any:
			for key, child := range node {
				path, ok := child.(string)
				file := key == "certificate_path" || key == "key_path" || (key == "path" && (node["type"] == "local" || (node["type"] == nil && node["tag"] != nil && node["format"] != nil)))
				if ok && file && path != "" {
					if filepath.Base(path) == "auto-proxy-rules.json" && (path == "auto-proxy-rules.json" || path == filepath.Join(sourceDir, "auto-proxy-rules.json") || path == filepath.Join(rulesDir, "auto-proxy-rules.json")) {
						node[key] = filepath.Join(liveDir, "auto-proxy-rules.json")
					} else if !filepath.IsAbs(path) {
						base := sourceDir
						if key == "path" {
							base = rulesDir
						}
						node[key] = filepath.Join(base, path)
					}
				} else {
					walk(child)
				}
			}
		case []any:
			for _, child := range node {
				walk(child)
			}
		}
	}
	walk(value)
	return json.MarshalIndent(value, "", "  ")
}
