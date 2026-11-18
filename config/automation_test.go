package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const automationYAML = `automation:
  - id: build-alert
    enabled: true
    criteria:
      chat_ids: [oc_quiet]
      sender_ids: [ou_a]
      message_types: [text]
      contains: [ready, complete]
      excludes: [cancelled]
    callback:
      executable: /usr/bin/true
      args: [--example]
      timeout: 30s
`

func TestLoad_ParsesAutomation(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(path, []byte(automationYAML), 0o600))
	cfg, err := Load(path)
	require.NoError(t, err)
	require.Len(t, cfg.Automation, 1)
	rule := cfg.Automation[0]
	require.Equal(t, "build-alert", rule.ID)
	require.True(t, rule.Enabled)
	require.Equal(t, []string{"oc_quiet"}, rule.Criteria.ChatIDs)
	require.Equal(t, []string{"ou_a"}, rule.Criteria.SenderIDs)
	require.Equal(t, []string{"text"}, rule.Criteria.MessageTypes)
	require.Equal(t, []string{"ready", "complete"}, rule.Criteria.Contains)
	require.Equal(t, []string{"cancelled"}, rule.Criteria.Excludes)
	require.Equal(t, "/usr/bin/true", rule.Callback.Executable)
	require.Equal(t, []string{"--example"}, rule.Callback.Args)
	require.Equal(t, 30*time.Second, rule.Callback.Timeout)
	require.Contains(t, Keys(), "automation")
}

func TestLoad_RejectsInvalidAutomation(t *testing.T) {
	t.Parallel()
	for name, rules := range map[string]string{
		"missing id":       `[{enabled: true, criteria: {chat_ids: [oc_quiet]}, callback: {executable: /usr/bin/true, timeout: 1s}}]`,
		"duplicate id":     `[{id: same, enabled: false, criteria: {chat_ids: [oc_quiet]}, callback: {executable: /usr/bin/true, timeout: 1s}}, {id: same, enabled: false, criteria: {chat_ids: [oc_quiet]}, callback: {executable: /usr/bin/true, timeout: 1s}}]`,
		"empty criteria":   `[{id: build-alert, enabled: true, callback: {executable: /usr/bin/true, timeout: 1s}}]`,
		"empty executable": `[{id: build-alert, enabled: true, criteria: {chat_ids: [oc_quiet]}, callback: {timeout: 1s}}]`,
		"zero timeout":     `[{id: build-alert, enabled: true, criteria: {chat_ids: [oc_quiet]}, callback: {executable: /usr/bin/true}}]`,
		"negative timeout": `[{id: build-alert, enabled: true, criteria: {chat_ids: [oc_quiet]}, callback: {executable: /usr/bin/true, timeout: -1s}}]`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.yaml")
			require.NoError(t, os.WriteFile(path, []byte("automation: "+rules+"\n"), 0o600))
			_, err := Load(path)
			require.ErrorContains(t, err, "automation")
			_, err = LoadWith(filepath.Join(t.TempDir(), "absent.yaml"), []string{"automation=" + rules})
			require.ErrorContains(t, err, "automation")
		})
	}
}

func TestConfig_EmptyAutomationRoundTrips(t *testing.T) {
	t.Parallel()
	cfg := Default()
	require.NoError(t, cfg.Set("automation", "[]"))
	require.Equal(t, Default(), cfg)
}
