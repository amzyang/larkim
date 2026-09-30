//go:build darwin

package larkcli

import (
	"context"
	"errors"
	"os"
	"path/filepath"
)

func masterKeyFile() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "Application Support", "lark-cli", "master.key.file"), nil
}

// MasterKeyMissing reports that lark-cli still reads its master key from the
// macOS keychain rather than master.key.file, which costs every subprocess
// several /usr/bin/security round trips.
func MasterKeyMissing() bool {
	p, err := masterKeyFile()
	if err != nil {
		return false
	}
	_, err = os.Stat(p)
	return errors.Is(err, os.ErrNotExist)
}

// KeychainDowngrade runs `lark-cli config keychain-downgrade`, moving the
// master key out of the keychain into master.key.file.
func (c *ExecClient) KeychainDowngrade(ctx context.Context) error {
	argv := []string{"config", "keychain-downgrade"}
	_, stderr, exitCode, err := c.exec(ctx, nil, argv...)
	if err != nil {
		return err
	}
	if exitCode != 0 {
		return decodeError(exitCode, stderr).withArgv(argv)
	}
	return nil
}
