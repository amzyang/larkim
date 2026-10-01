package main

import (
	"encoding/json/jsontext"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
)

// fetcher reads pinned npm files from a CDN through a cache on disk. A
// published npm version never changes, so the version in the cache path is the
// only invalidation there is: bumping a pin fetches fresh, and nothing else
// ever goes back to the network. Deleting the directory forces a refetch.
type fetcher struct {
	base   string // CDN root, e.g. https://cdn.jsdelivr.net/npm
	dir    string // cache root
	client *http.Client
}

// fetch returns path inside pkg, which is spelled name@version.
func (f fetcher) fetch(pkg, path string) ([]byte, error) {
	file := filepath.Join(f.dir, pkg, filepath.FromSlash(path))
	if b, err := os.ReadFile(file); err == nil {
		return b, nil
	}
	url := f.base + "/" + pkg + "/" + path
	resp, err := f.client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("get %s: %w", url, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", url, err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("get %s: %s", url, resp.Status)
	}
	// A CDN answers a throttled or broken request with an HTML page and a 200
	// often enough that the status alone cannot be trusted with the cache.
	if !jsontext.Value(b).IsValid() {
		return nil, fmt.Errorf("get %s: not JSON", url)
	}
	// Written aside and renamed into place, so an interrupted run leaves either
	// the whole file or none of it for the next run to trust.
	if err := os.MkdirAll(filepath.Dir(file), 0o755); err != nil {
		return nil, err
	}
	tmp := file + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, file); err != nil {
		return nil, err
	}
	return b, nil
}
