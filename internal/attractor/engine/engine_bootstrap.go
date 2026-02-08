package engine

import (
	"github.com/strongdm/kilroy/internal/attractor/model"
	"github.com/strongdm/kilroy/internal/attractor/runtime"
)

func newBaseEngine(g *model.Graph, dotSource []byte, opts RunOptions) *Engine {
	e := &Engine{
		Graph:       g,
		Options:     opts,
		DotSource:   append([]byte{}, dotSource...),
		LogsRoot:    opts.LogsRoot,
		WorktreeDir: opts.WorktreeDir,
		Context:     runtime.NewContext(),
		Registry:    NewDefaultRegistry(),
		Interviewer: &AutoApproveInterviewer{},
	}
	e.RunBranch = buildRunBranch(opts.RunBranchPrefix, opts.RunID)
	return e
}
