//go:build darwin

package larkcli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMasterKeyMissing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	require.True(t, MasterKeyMissing())

	dir := filepath.Join(home, "Library", "Application Support", "lark-cli")
	require.NoError(t, os.MkdirAll(dir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "master.key.file"), []byte("k"), 0o600))
	require.False(t, MasterKeyMissing())
}

func TestKeychainDowngrade_InvokesConfigSubcommand(t *testing.T) {
	t.Parallel()
	c := fakeBinary(t, `
case "$1 $2" in
"config keychain-downgrade") exit 0 ;;
*) echo "unexpected: $*" >&2; exit 1 ;;
esac`)
	require.NoError(t, c.KeychainDowngrade(t.Context()))
}
