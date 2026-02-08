package runtime

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

type FinalStatus string

const (
	FinalSuccess FinalStatus = "success"
	FinalFail    FinalStatus = "fail"
)

type FinalOutcome struct {
	Timestamp time.Time   `json:"timestamp"`
	Status    FinalStatus `json:"status"`

	RunID string `json:"run_id"`

	FinalGitCommitSHA string `json:"final_git_commit_sha"`
	FailureReason     string `json:"failure_reason,omitempty"`

	CXDBContextID  string `json:"cxdb_context_id"`
	CXDBHeadTurnID string `json:"cxdb_head_turn_id"`
}

func (fo *FinalOutcome) Save(path string) error {
	if fo == nil {
		return fmt.Errorf("final outcome is nil")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(fo, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}
