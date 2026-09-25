package emoji

import (
	"slices"
	"strings"
	"sync"

	"github.com/amzyang/larkim/fuzzy"
)

// extra is one Unicode emoji Feishu's own set has nothing for. A message can
// carry it as an ordinary character and every client draws it, but Feishu takes
// only its own keys as a reaction — hence NoReaction on every one of them.
//
// This is one person's working vocabulary, not a Unicode table: add what you
// reach for and nothing else. The names are what a query is matched against,
// through their pinyin and pinyin initials as well.
type extra struct {
	key, glyph string
	zh, en     string
	// alias is the further names this emoji answers to, beside zh and en.
	alias []string
}

var extras = []extra{
	{key: "white_check_mark", glyph: "✅", zh: "对勾", en: "check", alias: []string{"完成", "通过", "done"}},
	{key: "cross_mark", glyph: "❌", zh: "叉", en: "cross", alias: []string{"错误", "失败", "fail"}},
	{key: "warning", glyph: "⚠️", zh: "警告", en: "warning", alias: []string{"注意", "小心"}},
	{key: "question", glyph: "❓", zh: "问号", en: "question", alias: []string{"疑问"}},
	{key: "exclamation", glyph: "❗", zh: "感叹号", en: "exclamation", alias: []string{"重要"}},
	{key: "rocket", glyph: "🚀", zh: "火箭", en: "rocket", alias: []string{"上线", "发布", "launch", "ship"}},
	{key: "bug", glyph: "🐛", zh: "虫子", en: "bug", alias: []string{"缺陷", "问题"}},
	{key: "memo", glyph: "📝", zh: "备忘", en: "memo", alias: []string{"记录", "笔记", "note"}},
	{key: "link", glyph: "🔗", zh: "链接", en: "link", alias: []string{"关联"}},
	{key: "sparkles", glyph: "✨", zh: "闪亮", en: "sparkles", alias: []string{"新的", "new"}},
	{key: "dart", glyph: "🎯", zh: "靶心", en: "dart", alias: []string{"目标", "target"}},
	{key: "bar_chart", glyph: "📊", zh: "图表", en: "chart", alias: []string{"数据", "报表"}},
	{key: "chart_up", glyph: "📈", zh: "上涨", en: "trending up", alias: []string{"增长"}},
	{key: "chart_down", glyph: "📉", zh: "下跌", en: "trending down", alias: []string{"下降"}},
	{key: "lock", glyph: "🔒", zh: "锁", en: "lock", alias: []string{"权限", "安全"}},
	{key: "key", glyph: "🔑", zh: "钥匙", en: "key", alias: []string{"密钥", "凭据"}},
	{key: "package", glyph: "📦", zh: "包", en: "package", alias: []string{"发版", "构建", "build"}},
	{key: "wrench", glyph: "🔧", zh: "扳手", en: "wrench", alias: []string{"修复", "工具", "fix"}},
	{key: "test_tube", glyph: "🧪", zh: "试管", en: "test", alias: []string{"测试", "实验"}},
	{key: "broom", glyph: "🧹", zh: "扫帚", en: "broom", alias: []string{"清理", "cleanup"}},
	{key: "wastebasket", glyph: "🗑️", zh: "垃圾桶", en: "trash", alias: []string{"删除", "废弃"}},
	{key: "hourglass", glyph: "⏳", zh: "沙漏", en: "hourglass", alias: []string{"等待", "进行中", "wip"}},
	{key: "zap", glyph: "⚡", zh: "闪电", en: "zap", alias: []string{"性能", "快"}},
	{key: "robot", glyph: "🤖", zh: "机器人", en: "robot", alias: []string{"自动", "bot"}},
	{key: "eyes", glyph: "👀", zh: "看看", en: "eyes", alias: []string{"关注", "围观"}},
	{key: "star", glyph: "⭐", zh: "星", en: "star", alias: []string{"收藏", "重点"}},
	{key: "green_circle", glyph: "🟢", zh: "绿灯", en: "green", alias: []string{"正常", "通过"}},
	{key: "yellow_circle", glyph: "🟡", zh: "黄灯", en: "yellow", alias: []string{"风险", "注意"}},
	{key: "red_circle", glyph: "🔴", zh: "红灯", en: "red", alias: []string{"故障", "阻塞"}},
	{key: "see_no_evil", glyph: "🙈", zh: "捂眼", en: "see no evil", alias: []string{"不忍看", "尴尬"}},
}

// Common is the Unicode emoji beside Feishu's own. Their Order continues past
// the client's own panel, so an unqueried composer offers Feishu's first and
// these after them.
var Common = sync.OnceValue(func() []Emoji {
	out := make([]Emoji, 0, len(extras))
	for i, x := range extras {
		out = append(out, Emoji{
			Key: x.key, Glyph: x.glyph, ZH: x.zh, EN: x.en,
			Terms: commonTerms(x), Order: len(table) + 2 + i, NoReaction: true,
		})
	}
	return out
})

// commonTerms spells every name this emoji answers to the way it is typed on a
// latin keyboard. The key is in there too, because a shortcode is how one of
// these is reached from muscle memory.
func commonTerms(x extra) []string {
	var out []string
	add := func(s string) {
		for _, t := range fuzzy.Terms(s) {
			if t = strings.ToLower(t); t != "" && !slices.Contains(out, t) {
				out = append(out, t)
			}
		}
	}
	for _, name := range append([]string{x.zh, x.en, x.key}, x.alias...) {
		add(name)
	}
	return out
}
