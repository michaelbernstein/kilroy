package modeldb

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

// CatalogUpdatePolicy controls whether model metadata comes from a pinned file
// or a best-effort on-run-start fetch with fallback.
type CatalogUpdatePolicy string

const (
	CatalogPinnedOnly CatalogUpdatePolicy = "pinned"
	CatalogOnRunStart CatalogUpdatePolicy = "on_run_start"
)

// ResolvedCatalog describes the effective catalog snapshot used for a run.
type ResolvedCatalog struct {
	SnapshotPath string
	Source       string
	SHA256       string
	Warning      string
}

func fetchBytes(ctx context.Context, url string, timeout time.Duration) ([]byte, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(cctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, fmt.Errorf("status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return io.ReadAll(resp.Body)
}

func copyFile(dst, src string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o644)
}
