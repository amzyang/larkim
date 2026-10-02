package cli

import (
	"strings"
	"testing"

	"github.com/amzyang/larkim/ai"
	"github.com/stretchr/testify/require"
)

// The agent's history gate teaches ai.HistoryFlags as `messages list` flags;
// the command must actually carry each of them, taking a value, or a
// gate-approved invocation would die at the flag parser.
func TestMessagesList_CarriesTheAgentHistoryFlags(t *testing.T) {
	cmd := new(App).messagesListCmd()
	for _, f := range ai.HistoryFlags {
		flag := cmd.Flags().Lookup(strings.TrimPrefix(f.Name, "--"))
		require.NotNil(t, flag, "%s is not a messages list flag", f.Name)
		require.NotEqual(t, "bool", flag.Value.Type(), "%s must take a value", f.Name)
	}
}
