package mobilegen

import (
	"bytes"
	"encoding/json"
	"fmt"

	"github.com/sagernet/sing-box/common/srs"
)

// InspectRuleSetJSON exposes the compiled snapshot, including downloaded
// RULE-SET/GeoIP entries, without fetching or changing the running instance.
func InspectRuleSetJSON(data []byte) (string, error) {
	if len(data) == 0 || len(data) > 32<<20 {
		return "", fmt.Errorf("invalid rule-set size")
	}
	rules, err := srs.Read(bytes.NewReader(data), true)
	if err != nil {
		return "", fmt.Errorf("cannot read compiled rule set")
	}
	raw, err := json.Marshal(rules)
	if err != nil {
		return "", err
	}
	if len(raw) > 64<<20 {
		return "", fmt.Errorf("decoded rule set exceeds display limit")
	}
	return string(raw), nil
}
