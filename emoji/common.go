package emoji

import (
	"slices"
	"strings"
	"sync"

	"github.com/amzyang/larkim/fuzzy"
)

// extra is one emoji Feishu's own set has nothing for, as a Unicode character
// or, where no character says it, as the ASCII a keyboard draws it with. A
// message carries either as ordinary text and every client shows it, but Feishu
// takes only its own keys as a reaction — hence NoReaction on every one of them.
//
// This is one person's working vocabulary, not a Unicode table: add what you
// reach for and nothing else. The names are what a query is matched against,
// through their pinyin and pinyin initials as well.
//
// Three rules keep the list worth reading:
//
//   - One row per character. A character Feishu already draws belongs to its
//     own emoji, which can also be a reaction, so it is never added here; the
//     table in emoji_test.go fails the build on a second claim.
//   - One variant per emoji. Skin tone, hair, gender and colour variants
//     contribute their default form alone — the bare person over the gendered
//     pair, the unmodified hand over its five tones — because a variant on a
//     row of its own is a row the reader has to look past. A colour earns its
//     place only where it means something by itself, the way the three lights
//     read as 正常, 风险 and 故障 rather than as three circles.
//   - A name in both languages. An emoji nobody has a word for is one nobody
//     finds, however good the character is.
//
// Pinyin is spelled by go-pinyin at run time, a character at a time and the
// first reading of each, so a polyphone lands wrong — 重跑 comes out zhongpao.
// Where it does, the right reading goes in as an alias of its own.
type extra struct {
	key, glyph string
	zh, en     string
	// alias is the further names this emoji answers to, beside zh and en.
	alias []string
	// text is the ASCII this emoji is written as, for the ones no character
	// carries. An entry sets this or glyph, never both.
	text string
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
	{key: "red_circle", glyph: "🔴", zh: "红灯", en: "red", alias: []string{"故障", "阻塞", "zuse"}},
	{key: "see_no_evil", glyph: "🙈", zh: "捂眼", en: "see no evil", alias: []string{"不忍看", "尴尬"}},
	{key: "construction", glyph: "🚧", zh: "施工", en: "construction", alias: []string{"进行中", "未完", "wip"}},
	{key: "bandage", glyph: "🩹", zh: "创可贴", en: "bandage", alias: []string{"热修", "补丁", "hotfix"}},
	{key: "repeat", glyph: "🔁", zh: "重跑", en: "repeat", alias: []string{"重试", "chongpao", "chongshi", "retry", "rerun"}},
	{key: "pause", glyph: "⏸️", zh: "暂停", en: "pause", alias: []string{"挂起", "搁置", "hold"}},
	{key: "gear", glyph: "⚙️", zh: "齿轮", en: "gear", alias: []string{"配置", "设置", "config"}},
	{key: "file_cabinet", glyph: "🗄️", zh: "数据库", en: "database", alias: []string{"存储", "db"}},
	{key: "globe", glyph: "🌐", zh: "网络", en: "globe", alias: []string{"线上", "环境", "network"}},
	{key: "satellite", glyph: "📡", zh: "上报", en: "satellite", alias: []string{"接口", "埋点"}},
	{key: "shuffle", glyph: "🔀", zh: "合并", en: "shuffle", alias: []string{"分支", "merge", "branch"}},
	{key: "label", glyph: "🏷️", zh: "标签", en: "label", alias: []string{"版本", "tag"}},
	{key: "clipboard", glyph: "📋", zh: "需求", en: "clipboard", alias: []string{"清单", "待办", "checklist"}},
	{key: "puzzle", glyph: "🧩", zh: "模块", en: "puzzle", alias: []string{"依赖", "组件", "module"}},
	{key: "brick", glyph: "🧱", zh: "基建", en: "brick", alias: []string{"底座", "infra"}},
	{key: "wood", glyph: "🪵", zh: "日志", en: "wood", alias: []string{"打点", "log"}},
	{key: "magnifier", glyph: "🔍", zh: "排查", en: "magnifier", alias: []string{"查找", "定位", "search"}},
	{key: "compass", glyph: "🧭", zh: "方向", en: "compass", alias: []string{"规划", "roadmap"}},
	{key: "desktop", glyph: "🖥️", zh: "机器", en: "desktop", alias: []string{"服务器", "server"}},
	{key: "speech_balloon", glyph: "💬", zh: "评论", en: "speech balloon", alias: []string{"讨论", "comment"}},
	{key: "calendar", glyph: "📆", zh: "排期", en: "calendar", alias: []string{"日程", "schedule"}},
	{key: "graduation", glyph: "🎓", zh: "毕业", en: "graduation", alias: []string{"学员", "结业"}},
	{key: "books", glyph: "📚", zh: "课程", en: "books", alias: []string{"教材", "资料", "course"}},
	{key: "pencil", glyph: "✏️", zh: "作业", en: "pencil", alias: []string{"修改", "编辑", "edit"}},
	{key: "school", glyph: "🏫", zh: "校区", en: "school", alias: []string{"学校", "线下"}},
	{key: "teacher", glyph: "🧑‍🏫", zh: "老师", en: "teacher", alias: []string{"讲师", "授课"}},
	{key: "technologist", glyph: "🧑‍💻", zh: "研发", en: "technologist", alias: []string{"开发", "dev"}},
	{key: "mobile", glyph: "📲", zh: "端上", en: "mobile", alias: []string{"客户端", "app"}},
	{key: "balance", glyph: "⚖️", zh: "权衡", en: "balance", alias: []string{"取舍", "tradeoff"}},
	{key: "ice", glyph: "🧊", zh: "冻结", en: "ice", alias: []string{"封版", "freeze"}},
	{key: "turtle", glyph: "🐢", zh: "慢", en: "turtle", alias: []string{"性能", "卡顿", "slow"}},
	{key: "extinguisher", glyph: "🧯", zh: "灭火", en: "extinguisher", alias: []string{"止损", "应急"}},
	{key: "paperclip", glyph: "📎", zh: "附件", en: "paperclip", alias: []string{"关联", "attachment"}},
	{key: "white_circle", glyph: "⚪", zh: "未开始", en: "white", alias: []string{"待定", "空"}},
	{key: "saluting", glyph: "🫡", zh: "收到", en: "saluting", alias: []string{"明白", "roger"}},
	{key: "mind_blown", glyph: "🤯", zh: "震惊", en: "mind blown", alias: []string{"离谱", "崩溃"}},

	// ASCII, for the gestures that are typed rather than drawn: these go in as
	// the several cells of text they are, so they carry no glyph for an emoji's
	// column. The keys avoid Feishu's own spellings — it draws 🤷 as SHRUG —
	// because a key it already speaks would shadow its emoji. 颜文字 reaches the
	// four of them together, which is how one is looked for when the point is
	// the style rather than the gesture; where Feishu draws the same gesture,
	// as it does for 摊手, its own emoji leads and this follows it.
	{key: "shrug_text", text: `¯\_(ツ)_/¯`, zh: "摊手", en: "shrug",
		alias: []string{"耸肩", "无奈", "颜文字", "kaomoji"}},
	{key: "tableflip", text: "(╯°□°)╯︵ ┻━┻", zh: "掀桌", en: "tableflip",
		alias: []string{"掀桌子", "抓狂", "颜文字", "kaomoji"}},
	{key: "unflip", text: "┬─┬ノ( º _ ºノ)", zh: "扶桌", en: "unflip",
		alias: []string{"放回去", "冷静", "颜文字", "kaomoji"}},
	{key: "disapproval", text: "ಠ_ಠ", zh: "瞪", en: "disapproval",
		alias: []string{"无语", "鄙视", "颜文字", "kaomoji"}},
}

// Common is the Unicode emoji beside Feishu's own. Their Order continues past
// the client's own panel, so an unqueried composer offers Feishu's first and
// these after them.
var Common = sync.OnceValue(func() []Emoji {
	out := make([]Emoji, 0, len(extras))
	for i, x := range extras {
		out = append(out, Emoji{
			Key: x.key, Glyph: x.glyph, Insert: x.text, ZH: x.zh, EN: x.en,
			Terms: commonTerms(x), Order: len(table) + 2 + i, NoReaction: true,
		})
	}
	return out
})

// commonTerms spells every name this emoji answers to the way it is typed on a
// latin keyboard. The key is in there too, because a shortcode is how one of
// these is reached from muscle memory, and the character itself, because a
// reader who has it in the clipboard should not have to name it.
func commonTerms(x extra) []string {
	// The ASCII is not a term of its own: a query shaped like a face is one
	// nobody types, and the punctuation would not survive fuzzy.Terms anyway.
	var out []string
	if x.glyph != "" {
		out = append(out, x.glyph)
	}
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
