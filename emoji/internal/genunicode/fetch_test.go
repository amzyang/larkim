package main

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func serve(t *testing.T, status int, body string) (*atomic.Int32, fetcher) {
	var hits atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		assert.Equal(t, "/pkg@1.0.0/data/x.json", r.URL.Path)
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return &hits, fetcher{base: srv.URL, dir: t.TempDir(), client: srv.Client()}
}

func TestFetch_DownloadsOnceAndReadsTheCacheAfter(t *testing.T) {
	hits, f := serve(t, http.StatusOK, `{"a":1}`)

	b, err := f.fetch("pkg@1.0.0", "data/x.json")
	require.NoError(t, err)
	require.JSONEq(t, `{"a":1}`, string(b))
	require.FileExists(t, filepath.Join(f.dir, "pkg@1.0.0", "data", "x.json"))

	b, err = f.fetch("pkg@1.0.0", "data/x.json")
	require.NoError(t, err)
	require.JSONEq(t, `{"a":1}`, string(b))
	require.EqualValues(t, 1, hits.Load(), "a pinned version is fetched once")
}

func TestFetch_LeavesNoCacheEntryForABadAnswer(t *testing.T) {
	for name, tc := range map[string]struct {
		status int
		body   string
	}{
		"server error": {http.StatusInternalServerError, `{"a":1}`},
		"not json":     {http.StatusOK, `<html>rate limited</html>`},
	} {
		t.Run(name, func(t *testing.T) {
			_, f := serve(t, tc.status, tc.body)
			_, err := f.fetch("pkg@1.0.0", "data/x.json")
			require.Error(t, err)
			require.NoFileExists(t, filepath.Join(f.dir, "pkg@1.0.0", "data", "x.json"))
			require.NoFileExists(t, filepath.Join(f.dir, "pkg@1.0.0", "data", "x.json.tmp"))
		})
	}
}
