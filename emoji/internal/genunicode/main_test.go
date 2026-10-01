package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func fixture() sources {
	return sources{
		order: []string{"🚀", "👋", "🧑‍🤝‍🧑", "☺️"},
		meta: map[string]meta{
			"🚀":     {Name: "rocket", Slug: "rocket"},
			"👋":     {Name: "waving hand", Slug: "waving_hand", SkinTone: true},
			"🧑‍🤝‍🧑": {Name: "people holding hands", Slug: "people_holding_hands", SkinTone: true},
			"☺️":    {Name: "smiling face", Slug: "smiling_face"},
		},
		keywords: map[string][]string{"🚀": {"rocket", "launch"}, "👋": {"wave"}},
		zh: map[string]annotation{
			"🚀": {TTS: []string{"火箭"}, Default: []string{"发射", "火箭"}},
			"👋": {TTS: []string{"挥手"}, Default: []string{"你好"}},
			"☺": {TTS: []string{"微笑"}},
		},
		zhTone: map[string]annotation{
			"👋🏽":      {TTS: []string{"挥手: 中等肤色"}, Default: []string{"中等肤色", "你好"}},
			"👋🏻":      {TTS: []string{"挥手: 较浅肤色"}},
			"🧑🏻‍🤝‍🧑🏿": {TTS: []string{"手拉手的两个人: 较浅肤色较深肤色"}},
			"🧑🏽‍🤝‍🧑🏽": {TTS: []string{"手拉手的两个人: 中等肤色"}},
			"🚀🏽":      {TTS: []string{"not a sequence Unicode tones"}},
			"🇨🇳":      {TTS: []string{"中国"}},
		},
	}
}

func TestRows_BasesInCLDROrderThenTheirTones(t *testing.T) {
	var keys []string
	for _, r := range rows(fixture()) {
		keys = append(keys, r.key)
	}
	require.Equal(t, []string{
		"rocket", "waving_hand", "people_holding_hands", "smiling_face",
		"waving_hand_light_skin_tone", "waving_hand_medium_skin_tone",
		"people_holding_hands_light_skin_tone_dark_skin_tone",
		"people_holding_hands_medium_skin_tone",
	}, keys)
}

func TestRows_NameATonedSequenceTheWayCLDRDoes(t *testing.T) {
	byKey := map[string]row{}
	for _, r := range rows(fixture()) {
		byKey[r.key] = r
	}
	wave := byKey["waving_hand_medium_skin_tone"]
	require.Equal(t, "👋🏽", wave.glyph)
	require.Equal(t, "waving hand: medium skin tone", wave.en)
	require.Equal(t, "挥手: 中等肤色", wave.zh)
	require.Equal(t, []string{"中等肤色", "你好"}, wave.zhTags)
	require.Equal(t, []string{"wave"}, wave.tags, "a tone answers to its base's keywords")

	mixed := byKey["people_holding_hands_light_skin_tone_dark_skin_tone"]
	require.Equal(t, "people holding hands: light skin tone, dark skin tone", mixed.en)
}

func TestRows_ReadCLDRWithoutTheVariationSelector(t *testing.T) {
	r := rows(fixture())[3]
	require.Equal(t, "☺️", r.glyph, "the character keeps its selector")
	require.Equal(t, "微笑", r.zh)
}

func TestTerms_SpellTheNamesBeforeTheTags(t *testing.T) {
	sound := map[string][2]string{"火箭": {"huojian", "hj"}, "发射": {"fashe", "fs"}}
	r := row{glyph: "🚀", key: "rocket", en: "rocket", zh: "火箭", zhTags: []string{"发射", "火箭"}, tags: []string{"rocket", "launch"}}
	ts, n := terms(r, sound)
	require.Equal(t, []string{"🚀", "火箭", "huojian", "hj", "rocket", "发射", "fashe", "fs", "launch"}, ts)
	require.Equal(t, 5, n, "a tag that repeats a name is not a tag")
}

func TestKey_KeepsADigitSuffixClearOfFold(t *testing.T) {
	require.Equal(t, "keycap1", key("keycap_1"))
	require.Equal(t, "keycap10", key("keycap_10"))
	require.Equal(t, "waving_hand", key("waving_hand"))
}
