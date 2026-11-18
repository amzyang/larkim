package automation

import "time"

func QuotaRule(id, executable, chatID, senderID string) Rule {
	return Rule{ID: id, Criteria: Criteria{
		ChatIDs: []string{chatID}, SenderIDs: []string{senderID}, MessageTypes: []string{"interactive"},
		Contains: []string{"AI Coding 总额度已用", "再次申请加油包（+50%）"},
	}, Callback: Callback{Executable: executable, Args: []string{"automation", "claim-quota", "--chat-id", chatID, "--sender-id", senderID}, Timeout: 60 * time.Second}}
}

// QuotaScope recognizes only the built-in command shape. Callers still verify
// its executable identity before presenting it as a quota preset.
func QuotaScope(r Rule) (chatID, senderID string, ok bool) {
	a := r.Callback.Args
	if len(a) != 6 || a[0] != "automation" || a[1] != "claim-quota" || a[2] != "--chat-id" || a[4] != "--sender-id" || a[3] == "" || a[5] == "" {
		return "", "", false
	}
	if len(r.Criteria.ChatIDs) != 1 || len(r.Criteria.SenderIDs) != 1 || r.Criteria.ChatIDs[0] != a[3] || r.Criteria.SenderIDs[0] != a[5] {
		return "", "", false
	}
	return a[3], a[5], true
}
