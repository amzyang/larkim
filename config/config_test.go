package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestLoad_MissingFileGivesDefaults(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "nope.yaml"))
	require.NoError(t, err)
	require.Equal(t, 10*time.Second, cfg.PollInterval)
	require.Equal(t, int64(50<<20), cfg.Resources.MaxBytes)
	require.True(t, filepath.IsAbs(cfg.DataDir))
}

func TestLoad_OverridesAndFloors(t *testing.T) {
	p := filepath.Join(t.TempDir(), "c.yaml")
	require.NoError(t, os.WriteFile(p, []byte("poll_interval: 100ms\ndata_dir: ~/x\nbackfill_days: 7\nresources:\n  max_bytes: 1\n"), 0o644))
	cfg, err := Load(p)
	require.NoError(t, err)
	require.Equal(t, time.Second, cfg.PollInterval, "floored at 1s")
	require.Equal(t, 7, cfg.BackfillDays)
	require.Equal(t, int64(1), cfg.Resources.MaxBytes)
	require.NotContains(t, cfg.DataDir, "~")
	require.Equal(t, filepath.Join(cfg.DataDir, "larkim.db"), cfg.DBPath())
}
