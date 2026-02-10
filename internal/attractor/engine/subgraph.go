package engine

import (
	"context"
	"fmt"
	"strings"

	"github.com/strongdm/kilroy/internal/attractor/gitutil"
	"github.com/strongdm/kilroy/internal/attractor/runtime"
)

// runSubgraphUntil executes a subgraph starting at startNodeID and stops when the next hop would enter stopNodeID.
// The stop node itself is not executed. This is used to run parallel branches up to a shared fan-in node.
func runSubgraphUntil(ctx context.Context, eng *Engine, startNodeID, stopNodeID string) (parallelBranchResult, error) {
	if eng == nil || eng.Graph == nil {
		return parallelBranchResult{}, fmt.Errorf("subgraph engine is nil")
	}
	if strings.TrimSpace(startNodeID) == "" {
		return parallelBranchResult{}, fmt.Errorf("start node is required")
	}

	headSHA, _ := gitutil.HeadSHA(eng.WorktreeDir)

	current := startNodeID
	completed := []string{}
	nodeRetries := map[string]int{}

	var lastNode string
	var lastOutcome runtime.Outcome

	for {
		if err := ctx.Err(); err != nil {
			return parallelBranchResult{
				HeadSHA:    headSHA,
				LastNodeID: lastNode,
				Outcome:    lastOutcome,
				Completed:  completed,
			}, err
		}

		if strings.TrimSpace(stopNodeID) != "" && current == stopNodeID {
			return parallelBranchResult{
				HeadSHA:    headSHA,
				LastNodeID: lastNode,
				Outcome:    lastOutcome,
				Completed:  completed,
			}, nil
		}

		node := eng.Graph.Nodes[current]
		if node == nil {
			return parallelBranchResult{}, fmt.Errorf("missing node: %s", current)
		}

		eng.cxdbStageStarted(ctx, node)
		out, err := eng.executeWithRetry(ctx, node, nodeRetries)
		if err != nil {
			return parallelBranchResult{}, err
		}
		eng.cxdbStageFinished(ctx, node, out)
		if err := ctx.Err(); err != nil {
			return parallelBranchResult{
				HeadSHA:    headSHA,
				LastNodeID: lastNode,
				Outcome:    out,
				Completed:  completed,
			}, err
		}

		// Record completion.
		completed = append(completed, node.ID)

		// Apply context updates and built-ins.
		eng.Context.ApplyUpdates(out.ContextUpdates)
		eng.Context.Set("outcome", string(out.Status))
		eng.Context.Set("preferred_label", out.PreferredLabel)

		sha, err := eng.checkpoint(node.ID, out, completed, nodeRetries)
		if err != nil {
			return parallelBranchResult{}, err
		}
		eng.cxdbCheckpointSaved(ctx, node.ID, out.Status, sha)
		headSHA = sha
		lastNode = node.ID
		lastOutcome = out
		if err := ctx.Err(); err != nil {
			return parallelBranchResult{
				HeadSHA:    headSHA,
				LastNodeID: lastNode,
				Outcome:    lastOutcome,
				Completed:  completed,
			}, err
		}

		next, err := selectNextEdge(eng.Graph, node.ID, out, eng.Context)
		if err != nil {
			return parallelBranchResult{}, err
		}
		if next == nil {
			return parallelBranchResult{
				HeadSHA:    headSHA,
				LastNodeID: lastNode,
				Outcome:    lastOutcome,
				Completed:  completed,
			}, nil
		}
		if strings.TrimSpace(stopNodeID) != "" && next.To == stopNodeID {
			return parallelBranchResult{
				HeadSHA:    headSHA,
				LastNodeID: lastNode,
				Outcome:    lastOutcome,
				Completed:  completed,
			}, nil
		}
		if strings.EqualFold(next.Attr("loop_restart", "false"), "true") {
			return parallelBranchResult{}, fmt.Errorf("loop_restart not supported in v1")
		}
		if err := ctx.Err(); err != nil {
			return parallelBranchResult{
				HeadSHA:    headSHA,
				LastNodeID: lastNode,
				Outcome:    lastOutcome,
				Completed:  completed,
			}, err
		}
		current = next.To
	}
}
