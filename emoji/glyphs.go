package emoji

// glyphs is the Unicode column of the emoji table: the one thing the client's
// own assets cannot answer, because Feishu's API never sends a character.
//
// A key that has no faithful glyph maps to "" and is drawn as its own name or
// picture instead: an approximate face reads as the wrong feeling, where a
// name reads as an emoji this terminal cannot draw.
//
// Faithful is a high bar, and three kinds of near-miss do not clear it:
//   - the character means something else — 白眼 is not 🙂, 翻白眼 is not 🐶;
//   - Feishu draws a word and Unicode offers a shape — its OK, OKR, No, DONE
//     and -1 are bold lettering, not 👌🎯❌✅➖;
//   - the character has no colour of its own and lands as a thin monochrome
//     outline beside the emoji around it — ✔️ ✖️ 🎵 🔕 📢.
//
// Two emoji sharing one character is the same failure seen from the other
// side: whichever one a colleague chose, the strip shows the same thing.
//
// Keys are folded by Fold, so the `Lark_Emoji_Rose_0` spelling the client also
// sends lands on the same entry as `ROSE`.
var glyphs = map[string]string{
	"OK": "", "THUMBSUP": "👍", "THANKS": "🙏", "MUSCLE": "💪",
	"FINGERHEART": "🫰", "APPLAUSE": "👏", "FISTBUMP": "👊", "JIAYI": "",
	"DONE": "", "SMILE": "😊", "BLUSH": "😁", "LAUGH": "😄",
	"SMIRK": "😏", "LOL": "😂", "FACEPALM": "🤦", "LOVE": "😍",
	"WINK": "😉", "PROUD": "😤", "WITTY": "", "SMART": "🤓",
	"SCOWL": "", "THINKING": "🤔", "SOB": "😭", "CRY": "😢",
	"ERROR": "", "NOSEPICK": "", "HAUGHTY": "", "SLAP": "",
	"SPITBLOOD": "", "TOASTED": "", "GLANCE": "👀", "DULL": "",
	"INNOCENTSMILE": "😇", "JOYFUL": "😃", "WOW": "😮", "TRICK": "",
	"YEAH": "✌️", "ENOUGH": "", "TEARS": "🥲", "EMBARRASSED": "😅",
	"KISS": "😘", "SMOOCH": "💋", "DROOL": "🤤", "OBSESSED": "",
	"MONEY": "🤑", "TEASE": "😝", "SHOWOFF": "", "COMFORT": "",
	"CLAP": "", "PRAISE": "", "STRIVE": "", "XBLUSH": "😳",
	"SILENT": "🤐", "WAVE": "👋", "WHAT": "", "FROWN": "",
	"SHY": "", "DIZZY": "😵", "LOOKDOWN": "", "CHUCKLE": "🤭",
	"WAIL": "", "CRAZY": "🤪", "WHIMPER": "🥺", "HUG": "🤗",
	"BLUBBER": "", "WRONGED": "", "HUSKY": "", "SHHH": "🤫",
	"SMUG": "", "ANGRY": "😡", "HAMMER": "🔨", "SHOCKED": "😱",
	"TERROR": "😨", "PETRIFIED": "", "SKULL": "💀", "SWEAT": "😓",
	"SPEECHLESS": "😶", "SLEEP": "😴", "DROWSY": "😪", "YAWN": "🥱",
	"SICK": "🤒", "PUKE": "🤮", "BETRAYED": "", "HEADSET": "🎧",
	"EATINGFOOD": "", "MEMEME": "🙋", "SIGH": "😮‍💨", "TYPING": "",
	"LEMON": "🍋", "GET": "", "LGTM": "", "ONIT": "",
	"ONESECOND": "", "VRHEADSET": "🥽", "YOUARETHEBEST": "", "SALUTE": "",
	"SHAKE": "🤝", "HIGHFIVE": "🙌", "UPPERLEFT": "", "THUMBSDOWN": "👎",
	"SLIGHT": "", "TONGUE": "😛", "EYESCLOSED": "", "ROARFORYOU": "",
	"CALF": "🐮", "BEAR": "🐻", "BULL": "🐂", "RAINBOWPUKE": "",
	"ROSE": "🌹", "HEART": "❤️", "PARTY": "🎉", "LIPS": "",
	"BEER": "🍻", "CAKE": "🎂", "GIFT": "🎁", "CUCUMBER": "🥒",
	"DRUMSTICK": "", "PEPPER": "🌶️", "CANDIEDHAWS": "🍡", "BUBBLETEA": "🧋",
	"COFFEE": "☕", "YES": "", "NO": "", "OKR": "",
	"CHECKMARK": "", "CROSSMARK": "", "MINUSONE": "", "HUNDRED": "💯",
	"AWESOMEN": "", "PIN": "📌", "ALARM": "⏰", "LOUDSPEAKER": "",
	"TROPHY": "🏆", "FIRE": "🔥", "BOMB": "💣", "MUSIC": "",
	"XMASTREE": "🎄", "SNOWMAN": "⛄", "XMASHAT": "🎅", "FIREWORKS": "🎆",
	"REDPACKET": "🧧", "FORTUNE": "", "LUCK": "",
	"FIRECRACKER": "🧨", "STICKYRICEBALLS": "", "HEARTBROKEN": "💔", "POOP": "💩",
	"STATUSFLASHOFINSPIRATION": "💡", "18X": "🔞", "CLEAVER": "🔪", "SOCCER": "⚽",
	"BASKETBALL": "🏀", "GENERALDONOTDISTURB": "", "STATUS_PRIVATEMESSAGE": "",
	"GENERALINMEETINGBUSY": "📅", "STATUSREADING": "", "STATUSINFLIGHT": "✈️",
	"GENERALBUSINESSTRIP": "🧳", "GENERALWORKFROMHOME": "🏠", "STATUSENJOYLIFE": "🏖️",
	"GENERALTRAVELLINGCAR": "🚗", "STATUSBUS": "🚌", "GENERALSUN": "☀️",
	"GENERALMOONREST": "🌙", "MOONRABBIT": "🐰", "MOONCAKE": "🥮",
	"JUBILANTRABBIT": "🐇", "TV": "📺", "MOVIE": "🎬", "PUMPKIN": "🎃",
	"BEAMINGFACE": "", "DELIGHTED": "😀", "COLDSWEAT": "😰", "FULLMOONFACE": "🌝",
	"PARTYING": "🥳", "GOGOGO": "🏃", "THANKSFACE": "", "SALUTEFACE": "",
	"SHRUG": "🤷", "CLOWNFACE": "🤡", "HAPPYDRAGON": "🐲",

	// Spellings the client sends in message text that are not emoji the client
	// offers, so they carry no entry in table and live only here.
	"FIGHTING": "💪", "GRIN": "😁", "OKHAND": "👌",
}
