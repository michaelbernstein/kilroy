package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/danshapiro/kilroy/internal/attractor/ingest"
)

var osExecutable = os.Executable
var readBuildInfo = debug.ReadBuildInfo

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
		opts.skillPath = resolveDefaultIngestSkillPath(opts.repoPath)
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
		fmt.Fprintln(os.Stderr, "  --skill         Path to skill .md file (default: repo/binary auto-detect)")
		fmt.Fprintln(os.Stderr, "  --repo          Repository root (default: cwd)")
		fmt.Fprintln(os.Stderr, "  --max-turns     Max agentic turns for Claude (default: 15)")
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

func resolveDefaultIngestSkillPath(repoPath string) string {
	for _, candidate := range defaultIngestSkillCandidates(repoPath) {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	return ""
}

func defaultIngestSkillCandidates(repoPath string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, 6)
	add := func(p string) {
		p = strings.TrimSpace(p)
		if p == "" {
			return
		}
		abs, err := filepath.Abs(p)
		if err != nil {
			return
		}
		abs = filepath.Clean(abs)
		if seen[abs] {
			return
		}
		seen[abs] = true
		out = append(out, abs)
	}
	skillPathUnder := func(base string) string {
		return filepath.Join(base, "skills", "english-to-dotfile", "SKILL.md")
	}

	if strings.TrimSpace(repoPath) != "" {
		add(skillPathUnder(repoPath))
	}

	if exePath, err := osExecutable(); err == nil {
		exePath = filepath.Clean(strings.TrimSpace(exePath))
		if exePath != "" {
			if resolved, err := filepath.EvalSymlinks(exePath); err == nil && strings.TrimSpace(resolved) != "" {
				exePath = resolved
			}
			exeDir := filepath.Dir(exePath)
			add(skillPathUnder(exeDir))
			add(skillPathUnder(filepath.Dir(exeDir)))
			add(skillPathUnder(filepath.Join(filepath.Dir(exeDir), "share", "kilroy")))
		}
	}

	for _, moduleDir := range moduleCacheCandidateRootsForInstalledBinary() {
		add(skillPathUnder(moduleDir))
	}

	return out
}

func moduleCacheCandidateRootsForInstalledBinary() []string {
	info, ok := readBuildInfo()
	if !ok || info == nil {
		return nil
	}
	modulePath := strings.TrimSpace(info.Main.Path)
	moduleVersion := strings.TrimSpace(info.Main.Version)
	if modulePath == "" {
		return nil
	}
	cacheRoot := strings.TrimSpace(os.Getenv("GOMODCACHE"))
	if cacheRoot == "" {
		cacheRoot = defaultGoModCacheRoot()
	}
	if cacheRoot == "" {
		return nil
	}

	seen := map[string]bool{}
	out := make([]string, 0, 4)
	add := func(root string) {
		root = strings.TrimSpace(root)
		if root == "" || seen[root] {
			return
		}
		root = filepath.Clean(root)
		seen[root] = true
		out = append(out, root)
	}

	moduleRootPrefix := filepath.FromSlash(modulePath)
	if moduleVersion != "" && moduleVersion != "(devel)" {
		add(filepath.Join(cacheRoot, moduleRootPrefix+"@"+moduleVersion))
	}

	globPattern := filepath.Join(cacheRoot, moduleRootPrefix+"@*")
	matches, _ := filepath.Glob(globPattern)
	if len(matches) > 1 {
		sort.Slice(matches, func(i, j int) bool {
			iInfo, iErr := os.Stat(matches[i])
			jInfo, jErr := os.Stat(matches[j])
			if iErr != nil && jErr != nil {
				return matches[i] > matches[j]
			}
			if iErr != nil {
				return false // push stat-failed entries to end
			}
			if jErr != nil {
				return true
			}
			if !iInfo.ModTime().Equal(jInfo.ModTime()) {
				return iInfo.ModTime().After(jInfo.ModTime())
			}
			return matches[i] > matches[j]
		})
	}
	for _, m := range matches {
		add(m)
	}

	return out
}

func defaultGoModCacheRoot() string {
	gopath := strings.TrimSpace(os.Getenv("GOPATH"))
	if gopath != "" {
		if idx := strings.IndexRune(gopath, os.PathListSeparator); idx > 0 {
			gopath = gopath[:idx]
		}
	}
	if gopath == "" {
		home, err := os.UserHomeDir()
		if err != nil || strings.TrimSpace(home) == "" {
			return ""
		}
		gopath = filepath.Join(home, "go")
	}
	return filepath.Join(gopath, "pkg", "mod")
}

func runIngest(opts *ingestOptions) (string, error) {
	if strings.TrimSpace(opts.skillPath) == "" {
		candidates := defaultIngestSkillCandidates(opts.repoPath)
		if len(candidates) == 0 {
			return "", fmt.Errorf("no default skill file found; pass --skill <path>")
		}
		return "", fmt.Errorf("no default skill file found; checked: %s; pass --skill <path>", strings.Join(candidates, ", "))
	}

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
