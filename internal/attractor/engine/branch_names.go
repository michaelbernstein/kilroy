package engine

import "strings"

func buildRunBranch(prefix, runID string) string {
	trimmedPrefix := strings.Trim(strings.TrimSpace(prefix), "/")
	trimmedRunID := strings.Trim(strings.TrimSpace(runID), "/")
	switch {
	case trimmedPrefix == "":
		return trimmedRunID
	case trimmedRunID == "":
		return trimmedPrefix
	default:
		return trimmedPrefix + "/" + trimmedRunID
	}
}

func buildParallelBranch(prefix, runID, fanNodeID, childNodeID string) string {
	runID = strings.Trim(strings.TrimSpace(runID), "/")
	fanNodeID = sanitizeRefComponent(fanNodeID)
	childNodeID = sanitizeRefComponent(childNodeID)
	parts := []string{"parallel"}
	for _, p := range []string{runID, fanNodeID, childNodeID} {
		if strings.TrimSpace(p) == "" {
			continue
		}
		parts = append(parts, p)
	}
	return buildRunBranch(prefix, strings.Join(parts, "/"))
}
