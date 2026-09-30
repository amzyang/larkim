//go:build darwin

package tui

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/amzyang/larkim/larkcli"
	"github.com/stretchr/testify/require"
)

func TestKeychainStartup_WarnsAndDowngradesWhenMasterKeyMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.True(t, larkcli.MasterKeyMissing())

	dir := filepath.Join(home, "Library", "Application Support", "lark-cli")
	require.NoError(t, os.MkdirAll(dir, 0o700))

	master := filepath.Join(dir, "master.key.file")
	script := `case "$1 $2" in
"config keychain-downgrade")
  echo secret > "` + master + `"
  exit 0
;;
*) echo "unexpected: $*" >&2; exit 1 ;;
esac`
	path := filepath.Join(t.TempDir(), "lark-cli")
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755))

	d := Deps{Client: &larkcli.ExecClient{Path: path, Timeout: 10 * time.Second}}
	cmd := keychainStartup(d)
	require.NotNil(t, cmd)
	m := cmd().(statusWarnMsg)
	require.Equal(t, keychainWarnOK, m.text)
	require.False(t, larkcli.MasterKeyMissing())
}

func TestKeychainStartup_SkipsWhenMasterKeyPresent(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	dir := filepath.Join(home, "Library", "Application Support", "lark-cli")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "master.key.file"), []byte("k"), 0o600))
	require.Nil(t, keychainStartup(Deps{}))
}
