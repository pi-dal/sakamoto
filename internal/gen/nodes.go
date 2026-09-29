package gen

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// NodeEntry includes a private raw share link for editing; never render Raw on screen.
type NodeEntry struct {
	Line           int
	Tag, Type, Raw string
}

func ParseNodeLink(raw string) (NodeEntry, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" || strings.ContainsAny(raw, "\r\n") {
		return NodeEntry{}, fmt.Errorf("a node share link must fit on one line")
	}
	nodes := parseShareLines([]string{raw})
	if len(nodes) != 1 {
		return NodeEntry{}, fmt.Errorf("unsupported or invalid node share link")
	}
	n := nodes[0]
	tag, _ := n["tag"].(string)
	kind, _ := n["type"].(string)
	server, _ := n["server"].(string)
	if tag == "" || kind == "" || server == "" {
		return NodeEntry{}, fmt.Errorf("node is missing a name or server")
	}
	if _, ok := n["server_port"]; !ok {
		if _, ok := n["server_ports"]; !ok {
			return NodeEntry{}, fmt.Errorf("node is missing a port")
		}
	}
	return NodeEntry{Tag: tag, Type: kind, Raw: raw}, nil
}
func ReadNodes(path string) ([]NodeEntry, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var result []NodeEntry
	for index, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		entry, err := ParseNodeLink(line)
		if err != nil {
			entry = NodeEntry{Tag: fmt.Sprintf("line %d: invalid node", index+1), Type: "invalid", Raw: line}
		}
		entry.Line = index + 1
		result = append(result, entry)
	}
	return result, nil
}

// ChangeNode atomically updates or deletes a node only if the line is unchanged.
// For append, pass line=0 and expected="". For delete, replacement="".
func ChangeNode(path string, line int, expected, replacement string) error {
	if replacement != "" {
		if _, err := ParseNodeLink(replacement); err != nil {
			return err
		}
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) && line == 0 {
		data = nil
		err = nil
	}
	if err != nil {
		return err
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(data) == 0 {
		lines = nil
	}
	if line == 0 {
		lines = append(lines, strings.TrimSpace(replacement))
	} else {
		if line < 1 || line > len(lines) || strings.TrimSpace(lines[line-1]) != expected {
			return fmt.Errorf("the node file changed; refresh before editing")
		}
		if replacement == "" {
			lines = append(lines[:line-1], lines[line:]...)
		} else {
			lines[line-1] = strings.TrimSpace(replacement)
		}
	}
	body := strings.Join(lines, "\n")
	if body != "" {
		body += "\n"
	}
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".nodes-*")
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(f.Name()) }()
	if err := f.Chmod(0600); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.WriteString(body); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
