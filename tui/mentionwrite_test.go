package tui

import (
	"testing"

	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/assert"
)

func roster(pairs ...string) []store.Contact {
	out := make([]store.Contact, 0, len(pairs)/2)
	for i := 0; i+1 < len(pairs); i += 2 {
		out = append(out, store.Contact{OpenID: pairs[i], Name: pairs[i+1]})
	}
	return out
}

// Typing a name is not enough: Feishu only notifies on the tag, so this is
// what makes an @ reach anybody.
func TestResolveMentions_TurnsANameIntoATag(t *testing.T) {
	got := resolveMentions("@张三 看下", nil, roster("ou_a", "张三"))

	assert.Equal(t, `<at user_id="ou_a">张三</at> 看下`, got)
}

// The picker's answer outranks the roster, which is what settles two people
// sharing a display name.
func TestResolveMentions_PickedOutranksTheRoster(t *testing.T) {
	picked := map[string]string{"张三": "ou_picked"}

	got := resolveMentions("@张三 看下", picked, roster("ou_a", "张三"))

	assert.Equal(t, `<at user_id="ou_picked">张三</at> 看下`, got)
}

// A name nobody in the chat answers to is text the reader typed, not a broken
// tag: sending it as one would name somebody at random.
func TestResolveMentions_UnknownNameStaysLiteral(t *testing.T) {
	got := resolveMentions("@查无此人 在吗", nil, roster("ou_a", "张三"))

	assert.Equal(t, "@查无此人 在吗", got)
}

func TestResolveMentions_AllReachesTheWholeRoom(t *testing.T) {
	got := resolveMentions("@All 发布了", nil, roster("ou_a", "张三"))

	assert.Equal(t, `<at user_id="all"></at> 发布了`, got)
}

// Resolution is by name rather than by offset, so editing the draft after
// picking cannot put the tag on the wrong word.
func TestResolveMentions_SurvivesEditingAroundTheName(t *testing.T) {
	picked := map[string]string{"张三": "ou_a"}

	got := resolveMentions("改完了，@张三 你再看一眼", picked, nil)

	assert.Equal(t, `改完了，<at user_id="ou_a">张三</at> 你再看一眼`, got)
}

// An address is not a mention.
func TestResolveMentions_LeavesAnEmailAlone(t *testing.T) {
	got := resolveMentions("写到 linlan@example.com 了", nil, roster("ou_a", "example"))

	assert.Equal(t, "写到 linlan@example.com 了", got)
}

// The longer name wins over the one it contains, the way the reading side
// already resolves them.
func TestResolveMentions_LongerNameWinsOverTheOneItContains(t *testing.T) {
	got := resolveMentions("@张三丰 在", nil, roster("ou_a", "张三", "ou_b", "张三丰"))

	assert.Equal(t, `<at user_id="ou_b">张三丰</at> 在`, got)
}

func TestResolveMentions_HandlesSeveralInOneDraft(t *testing.T) {
	got := resolveMentions("@张三 @李四 都看下", nil, roster("ou_a", "张三", "ou_b", "李四"))

	assert.Equal(t, `<at user_id="ou_a">张三</at> <at user_id="ou_b">李四</at> 都看下`, got)
}

// A name holding a space still resolves, since the picker can insert one.
func TestResolveMentions_NameWithASpace(t *testing.T) {
	got := resolveMentions("@Li Ming 看下", map[string]string{"Li Ming": "ou_a"}, nil)

	assert.Equal(t, `<at user_id="ou_a">Li Ming</at> 看下`, got)
}

func TestResolveMentions_DraftWithoutAnyAtIsUntouched(t *testing.T) {
	assert.Equal(t, "好的", resolveMentions("好的", nil, roster("ou_a", "张三")))
}

// A bot is in the roster and can be named like anyone else.
func TestResolveMentions_ReachesABot(t *testing.T) {
	bots := []store.Contact{{OpenID: "cli_c", Name: "构建机器人", IsBot: true}}

	got := resolveMentions("@构建机器人 重跑", nil, bots)

	assert.Equal(t, `<at user_id="cli_c">构建机器人</at> 重跑`, got)
}
