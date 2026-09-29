package tui

import (
	"cmp"
	"fmt"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/amzyang/larkim/ai"
	"github.com/amzyang/larkim/emoji"
	"github.com/amzyang/larkim/jev"
	"github.com/amzyang/larkim/store"
)

// The contextual row over the emoji grid has no counterpart in the Lark
// client: its reaction panel opens on a frequently used band and nothing in it
// reads the conversation, so there is no behaviour to copy and this is
// designed here. It is additive on purpose — the grid underneath keeps the
// frequency order the client's own panel opens with, the row is gone the
// moment a query narrows the picker, and every emoji in it is one the client
// would also take, so nothing the reader does through it is a thing the client
// could not have done.

// suggestOptions is how many emoji the answer chooses between. They are the
// head of the picker's own unqueried order — this reader's most used, then
// Feishu's panel order — so the set narrows onto what is actually reached for
// as the remembered list fills. The endpoint takes far more than this; the
// limit is the reader's own vocabulary, not the protocol's.
const suggestOptions = 40

// suggestRows is how many rows of the grid the answer is laid over, and so
// what it costs the composer's box.
const suggestRows = 3

// suggestPicks is how many cells that comes to across the picker's columns.
const suggestPicks = suggestRows * pickerCols

// suggestFloor is the probability under which a pick stops being a suggestion.
// Measured over six work messages the answer put its real candidates between
// 0.05 and 0.83 and everything past them under 0.03, and that tail is not
// harmless filler: on a colleague asking for a confirmation it held 烦躁, one
// keystroke away and dressed as something the conversation had chosen. A cell
// under the floor is filled from this reader's own order instead and carries
// no mark.
const suggestFloor = 0.05

// suggestContext is how many messages before the target go up with it. A
// reaction answers the message it is put on, and the few turns before it are
// what say whether that message is good news.
const suggestContext = 12

// suggestTimeout bounds the call. The row is best effort, so a long tail is
// dropped rather than waited out.
const suggestTimeout = 15 * time.Second

// suggestFits is the probability below which the row says nothing fits rather
// than naming three emoji anyway. It is a floor rather than a middle because
// the answer only separates one case sharply: a question aimed at the reader
// came back at 0.16, while everything measured that a reaction does suit —
// news at 0.73, a reported bug at 0.59, a request to confirm at 0.34, a line
// of chatter at 0.36 — sits above it with no gap between the good cases and
// the harmless ones. A higher floor takes the request to confirm with it, and
// that is the message this row is most useful on.
const suggestFits = 0.25

// suggestState is what the picker's first row is drawing.
type suggestState int

const (
	// suggestOff is no key configured: there is no row at all and the picker
	// is the one it has always been.
	suggestOff suggestState = iota
	suggestWaiting
	suggestReady
	// suggestNone is a message the answer says nobody would react to.
	suggestNone
	suggestFailed
)

// suggestion is an answer already had, kept so that reopening the picker on a
// message costs nothing. The cache is never emptied: a session's worth of
// these is a few keys per message reacted to, and a reader who comes back to a
// message wants the row that was there the first time.
type suggestion struct {
	picks []jev.Option
	state suggestState
}

// suggestedMsg carries an answer back to the picker that asked for it.
type suggestedMsg struct {
	gen  int64
	rank jev.Rank
	err  error
}

// askSuggest is the question the row is filled from: which of the emoji this
// reader actually reaches for fits the message the picker is open on, and
// whether that message is one to react to at all.
//
// The instructions are English against a transcript that is usually not,
// because English is where the model is strongest and the options are its
// vocabulary rather than the conversation's.
func (m Model) askSuggest(x store.Message) jev.Ask {
	opts := make(map[string]string, suggestOptions)
	for _, h := range m.emoji.Search("") {
		if len(opts) == suggestOptions {
			break
		}
		// An emoji Feishu refuses as a reaction is offered by the picker as a
		// picture to reply with, which is a message rather than a mark and not
		// what this row is holding out.
		if h.Emoji.Reactable() {
			opts[h.Emoji.Key] = emojiMeaning(h.Emoji)
		}
	}
	return jev.Ask{
		State: map[string]string{
			"me":     cmp.Or(m.selfName, m.deps.Self),
			"sender": cmp.Or(x.SenderName, x.SenderID),
			// The transcript is the chat page whatever pane the target came
			// from, so its pictures are the chat page's too.
			"transcript": ai.Transcript(m.suggestChatName(), m.beforeTarget(x), m.deps.Self, m.meta.imgText),
			"target":     ai.Line(x, m.deps.Self, m.metaFor(m.focus).imgText),
			"reactions":  standing(m.drawnChips(x)),
		},
		Pick:    suggestPick,
		Options: opts,
		Fits:    suggestFitsQ,
		True:    suggestFitsTrue,
		False:   suggestFitsFalse,
	}
}

// emojiMeaning names one option for the answer to judge it by. Both names go
// in because for a good part of Feishu's set the English one describes the
// picture and not the act — FoldedHands is 双手合十, which is said as thanks,
// Errr is 黑线, which is said as 无语, Roasted is 衰, which is bad luck. Asked
// with the English name alone the answer put 思考 on a reported bug and 泣不成声
// on a colleague saying they are worn out; with both it found 看 and 加油.
func emojiMeaning(e emoji.Emoji) string {
	if e.ZH == "" || e.ZH == e.EN {
		return cmp.Or(e.EN, e.Key)
	}
	return cmp.Or(e.EN, e.Key) + " / " + e.ZH
}

const (
	// The last two sentences are what the answers were measured on. Naming the
	// two spellings is what lets the Chinese one be read as the meaning, and
	// the vocabulary is what moved 撒花 to 鼓掌 on a release and 流泪 to 加油 on
	// a tired colleague. A clause telling it to prefer the restrained reaction
	// was tried and taken out again: it answered bad news with OK.
	suggestPick = "`me` is the person about to react, and `transcript` marks their own messages (me). " +
		"`sender` wrote `target`, the one message `me` is now putting an emoji reaction on. " +
		"Which of these emoji best fits as the reaction `me` would put on `target`? " +
		"A bracketed placeholder such as [Image] or [File] stands for a picture or attachment " +
		"whose content the text does not repeat; where a picture's writing could be read it follows " +
		"on a line of its own marked [image]. " +
		"Judge from what the conversation says, not from the order the emoji are listed in. " +
		"Each option is written as its English name and then the Chinese name the colleagues know " +
		"it by; where the English name only describes the picture, the Chinese name carries the meaning. " +
		"The chat is a Feishu group at a Chinese internet company and the people in it are colleagues, " +
		"so the reaction is a short answer between coworkers: it can be warm, wry or commiserating as " +
		"easily as approving, but it never mocks the sender. " +
		"The reactions such a group reaches for most are 赞 for agreement or praise, OK and 完成 for " +
		"'seen, done', 鼓掌 for a result worth celebrating, +1 for joining an opinion, 加油 for " +
		"encouragement, 看 for 'I am watching this', 双手合十 for thanks or please, and 捂脸/衰/黑线 " +
		"for something that went wrong. " +
		"`reactions` is what colleagues have already put on `target`: joining one that is already " +
		"there is an ordinary thing to do, and one marked as already `me`'s is one `me` cannot add " +
		"again. A message that names `me` directly usually wants an acknowledgement rather than a comment."
	suggestFitsQ = "Would `me` mark `target` with an emoji reaction, rather than reply in words or leave it alone?"
	// The true side is a list rather than a description because the narrow
	// version of it — news, results, decisions, jokes — put a reported bug
	// under the floor at 0.37 while the choice itself named BUG at 0.93.
	suggestFitsTrue = "`target` says something `me` can answer with a mark: news, a result, a decision, " +
		"a bug or problem someone reported, a request someone is waiting to see acknowledged, a joke, " +
		"or anything a colleague would want to show they have seen"
	suggestFitsFalse = "`target` needs words back — a direct question only an answer settles — " +
		"or it is routine chatter nobody marks"
)

// standing names the reactions already on the message, which is the strongest
// thing the answer has to go on: three colleagues on 赞 took 赞 from 0.46 to
// 0.99, and three on 鼓掌 took 鼓掌 from 0.29 to 0.99. A reader mostly joins
// what the room has already said.
//
// It is the strip as drawn rather than as stored, so a press still on its way
// to Feishu counts — the reader has made that choice whatever Feishu has
// answered yet.
func standing(chips []emoji.Chip) string {
	var parts []string
	for _, c := range chips {
		// A key no build of the table names is still worth naming: the key
		// spells the word the answer would have been given anyway.
		name := c.Key
		if e, ok := emoji.ByKey(c.Key); ok {
			name = emojiMeaning(e)
		}
		part := fmt.Sprintf("%s ×%d", name, c.Count)
		if c.Mine {
			part += " (mine already)"
		}
		parts = append(parts, part)
	}
	if len(parts) == 0 {
		return "none yet"
	}
	return strings.Join(parts, ", ")
}

// beforeTarget is the handful of messages the target answers. A target the
// open page does not carry — a thread reply read in the right column — leaves
// the page's own tail, which is still the conversation it happens in.
func (m Model) beforeTarget(x store.Message) []store.Message {
	i := slices.IndexFunc(m.msgs, func(y store.Message) bool { return y.MessageID == x.MessageID })
	if i < 0 {
		i = len(m.msgs)
	}
	return m.msgs[max(0, i-suggestContext):i]
}

// suggestChatName names the chat the transcript opens with.
func (m Model) suggestChatName() string {
	if c, ok := m.currentChat(); ok && c.Name != "" {
		return c.Name
	}
	return m.chatID
}

// armSuggest opens the contextual row against x, from the cache when the
// answer is already had. It returns the command that asks for one, nil when
// there is nothing to ask.
func (m *Model) armSuggest(x store.Message) tea.Cmd {
	if m.suggester == nil {
		return nil
	}
	if s, ok := m.suggestCache[x.MessageID]; ok {
		m.picker.suggest, m.picker.picks = s.state, s.picks
		return nil
	}
	m.picker.suggest = suggestWaiting
	m.suggestGen++
	return suggestCmd(m.deps, m.suggester, m.suggestGen, m.askSuggest(x))
}

// suggestCmd asks the question and hands the answer back under gen, which is
// how one that arrives after its picker closed is told from the one the
// picker now open is waiting for.
func suggestCmd(d Deps, s ReactSuggester, gen int64, ask jev.Ask) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := waited(suggestTimeout)
		defer cancel()
		r, err := s.Rank(ctx, ask)
		return suggestedMsg{gen: gen, rank: r, err: err}
	}
}

// onSuggested fills the row the open picker armed.
func (m Model) onSuggested(msg suggestedMsg) (tea.Model, tea.Cmd) {
	if msg.gen != m.suggestGen || m.mode != modeEmoji {
		return m, nil
	}
	if msg.err != nil {
		// The row is best effort, so a failure says so in the row itself and
		// nowhere else: taking the notification line for it would cost the
		// reader a message about something they did ask for. It is not
		// cached either — the next press is worth another try.
		m.deps.log().Error("jev suggest", "message", m.picker.target.MessageID, "err", msg.err)
		m.picker.suggest = suggestFailed
		m.pickerGrid()
		return m, nil
	}
	s := suggestion{state: suggestReady, picks: msg.rank.Options}
	if msg.rank.Fits < suggestFits {
		s = suggestion{state: suggestNone}
	}
	if m.suggestCache == nil {
		m.suggestCache = map[string]suggestion{}
	}
	m.suggestCache[m.picker.target.MessageID] = s
	// A cursor the reader has walked somewhere keeps the emoji it was walked
	// to; one still where the picker put it goes to the best of what the
	// answer chose, which is the cell it would have opened on had the answer
	// been there already.
	var was string
	if m.picker.moved {
		was = m.pickerCursorKey()
	}
	m.picker.suggest, m.picker.picks = s.state, s.picks
	m.pickerGrid()
	m.pickerSeek(was)
	return m, nil
}

// pickerGrid lays the hits the picker draws: what its query answers with, and
// ahead of them the pickerCols cells of the contextual row.
//
// The row is part of the hits rather than a band beside them so that the
// cursor, the scroll and the visible window need know nothing about it — and
// so that an answer landing moves nothing, because the cells are already there
// and filling them leaves the count of hits alone. Whether the row is laid at
// all is rowOpen's call.
func (m *Model) pickerGrid() {
	p := &m.picker
	p.hits = m.emoji.Search(p.input.Value())
	p.found, p.marked = len(p.hits), 0
	if !p.rowOpen() {
		return
	}
	// What the answer chose comes out of the grid and goes in front of it, so
	// the head of the picker reads as the conversation's picks and then this
	// reader's own order, and the count of hits never changes: no cell is
	// blank, and nothing below moves when the rows are laid.
	front := make([]emoji.Hit, 0, suggestPicks)
	for _, o := range p.picks {
		if len(front) == suggestPicks || o.P < suggestFloor {
			break
		}
		// A key this build has no entry for cannot be drawn or pressed. It is
		// skipped rather than left as a hole, which is what keeps the marked
		// cells the first ones in the row.
		if e, ok := m.emoji.ByKey(o.Key); ok {
			front = append(front, emoji.Hit{Emoji: e})
		}
	}
	p.marked = len(front)
	rest := slices.DeleteFunc(slices.Clone(p.hits), func(h emoji.Hit) bool {
		return slices.ContainsFunc(front, func(f emoji.Hit) bool { return f.Emoji.Key == h.Emoji.Key })
	})
	// The cells the answer had nothing confident to say about are filled from
	// the order the picker would have opened with anyway, unmarked. An empty
	// cell would cost a keystroke's worth of grid for nothing, and a cell
	// filled from the tail of the distribution is worse than empty: on a
	// colleague asking for a confirmation that tail held 烦躁.
	for len(front) < suggestPicks && len(rest) > 0 {
		front, rest = append(front, rest[0]), rest[1:]
	}
	p.hits = append(front, rest...)
}

// pickerSeek puts the cursor back on an emoji after the grid was laid again.
// An answer pulls what it chose up to the head of the grid, and without this
// the emoji under the cursor would be replaced by whichever one moved into its
// place — a reader mid-press would react with something they never chose.
func (m *Model) pickerSeek(key string) {
	if key == "" {
		return
	}
	if i := slices.IndexFunc(m.picker.hits, func(h emoji.Hit) bool { return h.Emoji.Key == key }); i >= 0 {
		m.picker.idx = i
	}
	row := m.picker.idx / pickerCols
	m.picker.top = clamp(m.picker.top, max(0, row-m.pickerRows()+1), row)
}

// pickerCursorKey is the emoji the cursor stands on, empty on a cell of the
// contextual row that has nothing in it yet.
func (m Model) pickerCursorKey() string {
	if m.picker.idx >= len(m.picker.hits) {
		return ""
	}
	return m.picker.hits[m.picker.idx].Emoji.Key
}

// rowOpen reports whether the answer is laid over the head of the grid. Only a
// landed answer is: the picker opens on exactly the grid it has always opened
// on, and the rows are rearranged under the query line's own note when the
// answer comes. Reserving them empty was tried and gives the reader three
// rows of dots to look past.
//
// A narrowed grid never carries them: once a query is typed the reader has
// said what they want, and the fuzzy score answers that better than the
// conversation can.
func (p picker) rowOpen() bool {
	return p.suggest == suggestReady && strings.TrimSpace(p.input.Value()) == ""
}

// suggested reports whether the hit at an absolute index is one the answer
// chose, rather than one of the cells filled from the reader's own order to
// finish the rows out.
func (p picker) suggested(abs int) bool { return p.rowOpen() && abs < p.marked }

// suggestNote is what the query line says of the row, and nothing at all once
// the row speaks for itself.
func (p picker) suggestNote() string {
	switch p.suggest {
	case suggestWaiting:
		return "jev…"
	case suggestNone:
		return "nothing to react to"
	case suggestFailed:
		return "jev unavailable"
	}
	return ""
}
