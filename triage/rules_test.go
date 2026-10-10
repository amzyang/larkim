package triage

import (
	"testing"

	"github.com/amzyang/larkim/config"
	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/require"
)

func text(id, chat, sender, body string) store.Message {
	return store.Message{MessageID: id, ChatID: chat, MsgType: "text", SenderID: sender, SenderType: "user",
		SenderName: "张三", ContentRaw: `{"text":"` + body + `"}`, Content: body, RenderedAt: 1}
}

var (
	group = store.Chat{ChatID: "oc_team", Name: "项目协作群", ChatMode: "group"}
	p2p   = store.Chat{ChatID: "oc_peer", Name: "张三", ChatMode: "p2p"}
)

func TestClassify_CreditsTheFirstReasonThatMatches(t *testing.T) {
	t.Parallel()
	rules := NewRules(config.Notifications{
		Watch:    config.Strings{"ou_boss", "oc_ops"},
		Keywords: config.Strings{"上线|故障"},
	})

	atMe := text("om_at", "oc_team", "ou_a", "@林岚 看下")
	atMe.MentionsJSON = `[{"id":"ou_self","key":"@_user_1","name":"林岚"}]`
	atAll := text("om_all", "oc_team", "ou_a", "@所有人 周五团建")
	atAll.MentionsJSON = `[{"id":"all","key":"@_all","name":"所有人"}]`
	bot := text("om_bot", "oc_team", "cli_c", "构建完成")
	bot.SenderType = "app"
	call := store.Message{MessageID: "om_call", ChatID: "oc_team", MsgType: "video_chat", SenderID: "ou_a",
		SenderType: "user", ContentRaw: `{"topic":"","meet_number":"100000000"}`, RenderedAt: 1}
	empty := text("om_empty", "oc_team", "ou_a", "")
	ops := store.Chat{ChatID: "oc_ops", Name: "平台组", ChatMode: "group"}
	// A p2p call is still a call: it outranks p2p in the reason it reports.
	callInP2P := call
	callInP2P.ChatID = "oc_peer"

	for name, tc := range map[string]struct {
		m       store.Message
		c       store.Chat
		level   Level
		reason  string
		decided bool
	}{
		"the reader's own message":         {text("om_mine", "oc_team", "ou_self", "好的"), group, Drop, "self", true},
		"a bot":                            {bot, group, Drop, "non-user", true},
		"nothing to read":                  {empty, group, Drop, "empty", true},
		"a call with no text":              {call, group, P0, "vc", true},
		"a call in p2p":                    {callInP2P, p2p, P0, "vc", true},
		"p2p":                              {text("om_dm", "oc_peer", "ou_a", "在吗"), p2p, P0, "p2p", true},
		"@me":                              {atMe, group, P0, "at-me", true},
		"@all is not @me":                  {atAll, group, "", "", false},
		"a watched person":                 {text("om_boss", "oc_team", "ou_boss", "进度？"), group, P0, "watch-user", true},
		"a watched chat":                   {text("om_ops", "oc_ops", "ou_a", "日常"), ops, P0, "watch-chat", true},
		"a keyword":                        {text("om_kw", "oc_team", "ou_a", "今晚上线"), group, P0, "keyword:上线|故障", true},
		"a group message nothing names me": {text("om_gray", "oc_team", "ou_a", "午饭吃啥"), group, "", "", false},
	} {
		t.Run(name, func(t *testing.T) {
			v, decided := rules.Classify(tc.m, tc.c, "ou_self")
			require.Equal(t, tc.level, v.Level)
			require.Equal(t, tc.reason, v.Reason)
			require.Equal(t, tc.decided, decided)
		})
	}
}
