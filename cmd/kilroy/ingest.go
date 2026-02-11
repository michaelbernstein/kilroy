package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/strongdm/kilroy/internal/attractor/ingest"
)

type ingestOptions struct {
	requirements string
	outputPath   string
	model        string
	skillPath    string
	repoPath     string
	validate     bool
	maxTurns     int
}

func parseIngestArgs(args []string) (*ingestOptions, error) {
	opts := &ingestOptions{
		model:    "claude-sonnet-4-5",
		validate: true,
	}

	var positional []string
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--output", "-o":
			i++
			if i >= len(args) {
				return nil, fmt.Errorf("--output requires a value")
			}
			opts.outputPath = args[i]
		case "--model":
			i++
			if i >= len(args) {
				return nil, fmt.Errorf("--model requires a value")
			}
			opts.model = args[i]
		case "--skill":
			i++
			if i >= len(args) {
				return nil, fmt.Errorf("--skill requires a value")
			}
			opts.skillPath = args[i]
		case "--repo":
			i++
			if i >= len(args) {
				return nil, fmt.Errorf("--repo requires a value")
			}
			opts.repoPath = args[i]
		case "--max-turns":
			i++
			if i >= len(args) {
				return nil, fmt.Errorf("--max-turns requires a value")
			}
			n, err := strconv.Atoi(args[i])
			if err != nil || n < 1 {
				return nil, fmt.Errorf("--max-turns must be a positive integer")
			}
			opts.maxTurns = n
		case "--no-validate":
			opts.validate = false
		default:
			if strings.HasPrefix(args[i], "-") {
				return nil, fmt.Errorf("unknown flag: %s", args[i])
			}
			positional = append(positional, args[i])
		}
	}

	if len(positional) == 0 {
		return nil, fmt.Errorf("requirements text is required (positional argument)")
	}
	opts.requirements = strings.Join(positional, " ")

	if opts.repoPath == "" {
		cwd, err := os.Getwd()
		if err != nil {
			return nil, err
		}
		opts.repoPath = cwd
	}

	if opts.skillPath == "" {
		candidate := filepath.Join(opts.repoPath, "skills", "english-to-dotfile", "SKILL.md")
		if _, err := os.Stat(candidate); err == nil {
			opts.skillPath = candidate
		}
	}

	return opts, nil
}

func attractorIngest(args []string) {
	opts, err := parseIngestArgs(args)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		fmt.Fprintln(os.Stderr)
		fmt.Fprintln(os.Stderr, "usage: kilroy attractor ingest [flags] <requirements>")
		fmt.Fprintln(os.Stderr, "  --output, -o    Output .dot file path (default: stdout)")
		fmt.Fprintln(os.Stderr, "  --model         LLM model (default: claude-sonnet-4-5)")
		fmt.Fprintln(os.Stderr, "  --skill         Path to skill .md file (default: auto-detect)")
		fmt.Fprintln(os.Stderr, "  --repo          Repository root (default: cwd)")
		fmt.Fprintln(os.Stderr, "  --no-validate   Skip .dot validation")
		os.Exit(1)
	}

	dotContent, err := runIngest(opts)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	if opts.outputPath != "" {
		if err := os.WriteFile(opts.outputPath, []byte(dotContent), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Fprintf(os.Stderr, "wrote %s (%d bytes)\n", opts.outputPath, len(dotContent))
	} else {
		fmt.Print(dotContent)
	}
}

func runIngest(opts *ingestOptions) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()

	result, err := ingest.Run(ctx, ingest.Options{
		Requirements: opts.requirements,
		SkillPath:    opts.skillPath,
		Model:        opts.model,
		RepoPath:     opts.repoPath,
		Validate:     opts.validate,
		MaxTurns:     opts.maxTurns,
	})
	if err != nil {
		return "", err
	}

	for _, w := range result.Warnings {
		fmt.Fprintf(os.Stderr, "warning: %s\n", w)
	}

	return result.DotContent, nil
}
