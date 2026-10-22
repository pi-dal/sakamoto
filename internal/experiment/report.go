package experiment

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

const ReportFileName = "learning-status.json"

type LearningResult struct {
	Candidate
	Time   time.Time   `json:"time"`
	State  string      `json:"state"`
	Detail string      `json:"detail"`
	Direct ProbeResult `json:"direct"`
	Proxy  ProbeResult `json:"proxy"`
}

// Record keeps a bounded, private explanation history; no URLs, credentials or
// response bodies are stored. Only the single watcher writes this file.
func Record(dir string, result LearningResult) error {
	history, err := ReadReport(dir)
	if err != nil {
		return err
	}
	history = append(history, result)
	if len(history) > 100 {
		history = history[len(history)-100:]
	}
	raw, err := json.MarshalIndent(history, "", "  ")
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(dir, ReportFileName), raw)
}

func ReadReport(dir string) ([]LearningResult, error) {
	raw, err := os.ReadFile(filepath.Join(dir, ReportFileName))
	if os.IsNotExist(err) {
		return []LearningResult{}, nil
	}
	if err != nil {
		return nil, err
	}
	var history []LearningResult
	if err = json.Unmarshal(raw, &history); err != nil {
		return nil, fmt.Errorf("read learning explanations: %w", err)
	}
	return history, nil
}
