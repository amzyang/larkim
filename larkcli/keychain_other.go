//go:build !darwin

package larkcli

import "context"

// MasterKeyMissing is always false off macOS: lark-cli only stores its master
// key in the macOS keychain there.
func MasterKeyMissing() bool { return false }

// KeychainDowngrade is a no-op off macOS.
func (c *ExecClient) KeychainDowngrade(context.Context) error { return nil }
