package tui

import (
	"regexp"
	"strings"
)

// Feishu spells an official emoji two ways, and both reach the rendered text:
// a rich-text post carries an emotion element, which lark-cli writes as the
// emoji_type key between colons, while a plain text message carries the
// emoji's display name in brackets, in whichever language the sender's client
// was set to. Anything else shaped like either — a card icon name, a clock
// time, a bracketed noun — is left alone by expandEmoji.
var (
	shortcode   = regexp.MustCompile(`:([A-Za-z0-9_]{1,32}):`)
	bracketName = regexp.MustCompile(`\[([^\[\]\n]{1,12})\]`)
)

// officialEmoji maps Feishu's emoji_type keys to the closest Unicode glyph.
// A key that has no faithful glyph maps to "" and is drawn as its own name
// instead: an approximate face reads as the wrong feeling, where a name reads
// as an emoji this terminal cannot draw.
//
// Keys are normalized by emojiKey, so the `Lark_Emoji_Rose_0` spelling the
// client also sends lands on the same entry as `ROSE`.
var officialEmoji = map[string]string{
	"OK": "👌", "THUMBSUP": "👍", "THANKS": "🙏", "MUSCLE": "💪",
	"FINGERHEART": "🫰", "APPLAUSE": "👏", "FISTBUMP": "👊", "JIAYI": "",
	"DONE": "✅", "SMILE": "😊", "BLUSH": "☺️", "LAUGH": "😄",
	"SMIRK": "😏", "LOL": "😂", "FACEPALM": "🤦", "LOVE": "😍",
	"WINK": "😉", "PROUD": "😤", "WITTY": "😜", "SMART": "🤓",
	"SCOWL": "😠", "THINKING": "🤔", "SOB": "😭", "CRY": "😢",
	"ERROR": "", "NOSEPICK": "", "HAUGHTY": "", "SLAP": "",
	"SPITBLOOD": "", "TOASTED": "", "GLANCE": "👀", "DULL": "😑",
	"INNOCENTSMILE": "😇", "JOYFUL": "😃", "WOW": "😮", "TRICK": "😈",
	"YEAH": "✌️", "ENOUGH": "", "TEARS": "🥲", "EMBARRASSED": "😅",
	"KISS": "😘", "SMOOCH": "💋", "DROOL": "🤤", "OBSESSED": "",
	"MONEY": "🤑", "TEASE": "😝", "SHOWOFF": "", "COMFORT": "",
	"CLAP": "👏", "PRAISE": "", "STRIVE": "", "XBLUSH": "😳",
	"SILENT": "🤐", "WAVE": "👋", "WHAT": "", "FROWN": "☹️",
	"SHY": "", "DIZZY": "😵", "LOOKDOWN": "", "CHUCKLE": "🤭",
	"WAIL": "", "CRAZY": "🤪", "WHIMPER": "🥺", "HUG": "🤗",
	"BLUBBER": "", "WRONGED": "", "HUSKY": "🐶", "SHHH": "🤫",
	"SMUG": "", "ANGRY": "😡", "HAMMER": "🔨", "SHOCKED": "😱",
	"TERROR": "😨", "PETRIFIED": "🗿", "SKULL": "💀", "SWEAT": "😓",
	"SPEECHLESS": "😶", "SLEEP": "😴", "DROWSY": "😪", "YAWN": "🥱",
	"SICK": "🤒", "PUKE": "🤮", "BETRAYED": "", "HEADSET": "🎧",
	"EATINGFOOD": "🍚", "MEMEME": "🙋", "SIGH": "😮‍💨", "TYPING": "⌨️",
	"LEMON": "🍋", "GET": "", "LGTM": "", "ONIT": "",
	"ONESECOND": "", "VRHEADSET": "🥽", "YOUARETHEBEST": "", "SALUTE": "🫡",
	"SHAKE": "🤝", "HIGHFIVE": "🙌", "UPPERLEFT": "↖️", "THUMBSDOWN": "👎",
	"SLIGHT": "🙂", "TONGUE": "😛", "EYESCLOSED": "", "ROARFORYOU": "",
	"CALF": "🐮", "BEAR": "🐻", "BULL": "🐂", "RAINBOWPUKE": "",
	"ROSE": "🌹", "HEART": "❤️", "PARTY": "🎉", "LIPS": "",
	"BEER": "🍻", "CAKE": "🎂", "GIFT": "🎁", "CUCUMBER": "🥒",
	"DRUMSTICK": "🍗", "PEPPER": "🌶️", "CANDIEDHAWS": "🍡", "BUBBLETEA": "🧋",
	"COFFEE": "☕", "YES": "✅", "NO": "❌", "OKR": "🎯",
	"CHECKMARK": "✔️", "CROSSMARK": "✖️", "MINUSONE": "➖", "HUNDRED": "💯",
	"AWESOMEN": "", "PIN": "📌", "ALARM": "⏰", "LOUDSPEAKER": "📢",
	"TROPHY": "🏆", "FIRE": "🔥", "BOMB": "💣", "MUSIC": "🎵",
	"XMASTREE": "🎄", "SNOWMAN": "⛄", "XMASHAT": "🎅", "FIREWORKS": "🎆",
	"2022": "", "REDPACKET": "🧧", "FORTUNE": "", "LUCK": "🍀",
	"FIRECRACKER": "🧨", "STICKYRICEBALLS": "", "HEARTBROKEN": "💔", "POOP": "💩",
	"STATUSFLASHOFINSPIRATION": "💡", "18X": "🔞", "CLEAVER": "🔪", "SOCCER": "⚽",
	"BASKETBALL": "🏀", "GENERALDONOTDISTURB": "🔕", "STATUS_PRIVATEMESSAGE": "💬",
	"GENERALINMEETINGBUSY": "📅", "STATUSREADING": "📖", "STATUSINFLIGHT": "✈️",
	"GENERALBUSINESSTRIP": "🧳", "GENERALWORKFROMHOME": "🏠", "STATUSENJOYLIFE": "🏖️",
	"GENERALTRAVELLINGCAR": "🚗", "STATUSBUS": "🚌", "GENERALSUN": "☀️",
	"GENERALMOONREST": "🌙", "MOONRABBIT": "🐰", "MOONCAKE": "🥮",
	"JUBILANTRABBIT": "🐇", "TV": "📺", "MOVIE": "🎬", "PUMPKIN": "🎃",
	"BEAMINGFACE": "😁", "DELIGHTED": "😀", "COLDSWEAT": "😰", "FULLMOONFACE": "🌝",
	"PARTYING": "🥳", "GOGOGO": "🏃", "THANKSFACE": "", "SALUTEFACE": "",
	"SHRUG": "🤷", "CLOWNFACE": "🤡", "HAPPYDRAGON": "🐲",

	// Spellings the client sends that are not in the reaction list.
	"FIGHTING": "💪", "GRIN": "😁",
}

// emojiByName maps the display names a Chinese client writes into a text
// message onto the same glyphs. Only names that are unmistakably an emoji
// belong here: a bracketed word that is not in this table is ordinary text and
// stays as it is.
var emojiByName = map[string]string{
	"赞": "👍", "玫瑰": "🌹", "爱心": "❤️", "心碎": "💔", "双手合十": "🙏",
	"鼓掌": "👏", "撒花": "🎉", "捂脸": "🤦", "笑哭": "😂", "微笑": "😊",
	"大哭": "😭", "加油": "💪", "握手": "🤝", "抱抱": "🤗", "击掌": "🙌",
	"比心": "🫰", "耶": "✌️", "敬礼": "🫡", "奶茶": "🧋", "咖啡": "☕",
	"蛋糕": "🎂", "礼物": "🎁", "奖杯": "🏆", "闹钟": "⏰", "干杯": "🍻",
	"玫瑰花": "🌹", "发怒": "😡", "惊讶": "😮", "思考": "🤔", "偷笑": "🤭",
	"调皮": "😜", "汗": "😓", "睡觉": "😴", "呲牙": "😁", "机智": "🤓",
	"OK": "👌",
}

// emojiKey folds the spellings of one emoji onto a single lookup key: the
// client sends both the bare `Rose` and the `Lark_Emoji_Rose_0` form.
func emojiKey(s string) string {
	s = strings.ToUpper(s)
	s = strings.TrimPrefix(s, "LARK_EMOJI_")
	if i := strings.LastIndex(s, "_"); i > 0 && strings.Trim(s[i+1:], "0123456789") == "" {
		s = s[:i]
	}
	return s
}

// expandEmoji draws Feishu's official emoji. A key with no glyph of its own
// becomes its bracketed name, the way the Feishu client writes an emoji it
// cannot draw inline, and anything that is not an official emoji is left
// exactly as it came.
func expandEmoji(s string) string {
	if strings.Contains(s, ":") {
		s = shortcode.ReplaceAllStringFunc(s, func(m string) string {
			glyph, ok := officialEmoji[emojiKey(m[1:len(m)-1])]
			switch {
			case !ok:
				return m
			case glyph == "":
				return stDim.Render("[" + m[1:len(m)-1] + "]")
			default:
				return glyph
			}
		})
	}
	if !strings.Contains(s, "[") {
		return s
	}
	return bracketName.ReplaceAllStringFunc(s, func(m string) string {
		if glyph := namedEmoji(m[1 : len(m)-1]); glyph != "" {
			return glyph
		}
		return m
	})
}

// namedEmoji resolves the bracketed form, which carries a display name rather
// than an emoji_type key: a Chinese name, or the key itself on an English
// client.
func namedEmoji(name string) string {
	if glyph, ok := emojiByName[name]; ok {
		return glyph
	}
	return officialEmoji[emojiKey(name)]
}
