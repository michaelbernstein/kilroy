package ingest

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/strongdm/kilroy/internal/attractor/engine"
)

// Options configures an ingestion run.
type Options struct {
	Requirements string // The English requirements text.
	SkillPath    string // Path to the SKILL.md file.
	Model        string // LLM model ID.
	RepoPath     string // Repository root (working directory for claude).
	Validate     bool   // Whether to validate the .dot output.
	MaxTurns     int    // Max turns for claude (default 3).
}

// Result contains the output of an ingestion run.
type Result struct {
	DotContent string   // The extracted .dot file content.
	RawOutput  string   // The full raw output from Claude Code.
	Warnings   []string // Any validation warnings.
}

// wrapPrompt wraps raw requirements in explicit programmatic-mode instructions
// so Claude generates a DOT pipeline file instead of implementing the software.
func wrapPrompt(requirements, repoPath string) string {
	return fmt.Sprintf(`You are running in PROGRAMMATIC CLI INGEST MODE.

Your task: generate a Graphviz .dot pipeline file for Kilroy's Attractor engine.
You MUST follow the english-to-dotfile skill in your system prompt.

CRITICAL RULES:
- You are in programmatic mode (cannot ask questions). Default to Medium option.
- Output ONLY the raw .dot digraph content. No markdown fences, no explanatory text.
- DO NOT implement the software. DO NOT write code files. ONLY produce the .dot pipeline.
- The output must start with "digraph" and end with the closing "}".
- You may read files in the repository at %s to understand the project structure.
- You may use curl/WebFetch to fetch the weather report and LiteLLM catalog as described in the skill.

REQUIREMENTS:
%s`, repoPath, requirements)
}

func buildCLIArgs(opts Options) (string, []string, string) {
	exe := envOr("KILROY_CLAUDE_PATH", "claude")
	maxTurns := opts.MaxTurns
	if maxTurns <= 0 {
		maxTurns = 15
	}

	args := []string{
		"-p",
		"--output-format", "text",
		"--model", opts.Model,
		"--max-turns", fmt.Sprintf("%d", maxTurns),
		"--dangerously-skip-permissions",
		"--disallowedTools", "Write,Edit,NotebookEdit",
	}

	// Give Claude read access to the repo without running inside it.
	if opts.RepoPath != "" {
		args = append(args, "--add-dir", opts.RepoPath)
	}

	if opts.SkillPath != "" {
		skillContent, err := os.ReadFile(opts.SkillPath)
		if err == nil && len(skillContent) > 0 {
			args = append(args, "--append-system-prompt", string(skillContent))
		}
	}

	// Create a temp working directory so Claude can't write into the repo.
	tmpDir, err := os.MkdirTemp("", "kilroy-ingest-*")
	if err != nil {
		tmpDir = os.TempDir()
	}

	// The wrapped prompt is appended last.
	args = append(args, wrapPrompt(opts.Requirements, opts.RepoPath))

	return exe, args, tmpDir
}

// Run executes the ingestion: invokes Claude Code with the skill and requirements,
// extracts the .dot content, and optionally validates it.
func Run(ctx context.Context, opts Options) (*Result, error) {
	// Verify skill file exists.
	if _, err := os.Stat(opts.SkillPath); err != nil {
		return nil, fmt.Errorf("skill file not found: %s: %w", opts.SkillPath, err)
	}

	exe, args, tmpDir := buildCLIArgs(opts)
	defer os.RemoveAll(tmpDir)

	cmd := exec.CommandContext(ctx, exe, args...)
	cmd.Dir = tmpDir
	cmd.Stdin = strings.NewReader("")

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	rawOutput := stdout.String()
	if err != nil {
		return nil, fmt.Errorf("claude invocation failed (exit %v): %s\nstderr: %s",
			err, truncateStr(rawOutput, 500), truncateStr(stderr.String(), 500))
	}

	// Extract the digraph from the output.
	dotContent, err := ExtractDigraph(rawOutput)
	if err != nil {
		return nil, fmt.Errorf("failed to extract digraph from output: %w\nraw output (first 1000 chars): %s",
			err, truncateStr(rawOutput, 1000))
	}

	result := &Result{
		DotContent: dotContent,
		RawOutput:  rawOutput,
	}

	// Optionally validate.
	if opts.Validate {
		_, diags, err := engine.Prepare([]byte(dotContent))
		if err != nil {
			return result, fmt.Errorf("generated .dot failed validation: %w", err)
		}
		for _, d := range diags {
			result.Warnings = append(result.Warnings, fmt.Sprintf("%s: %s (%s)", d.Severity, d.Message, d.Rule))
		}
	}

	return result, nil
}

func envOr(key, def string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	return v
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}
