# What a post `md` element does with markdown

`larkim send --markdown` puts a body inside `{"tag":"md","text":…}` post elements
(`larkmd/post.go`, `PostContent`), all but the lines that carry an emoji — see
[Emoji](#emoji). This records what Feishu does with what is inside one, so a lint rule
cites an observation rather than the interactive-card documentation, which describes a
different renderer.

## How to read it again

Feishu stores a post twice: `content_v2` holds the `md` element as it was sent, and
`content` holds Feishu's own expansion of it into post elements. The expansion is what
the table below is read off.

```sh
larkim --config ./dev.yaml send --chat <a chat from CLAUDE.local.md> --markdown @probe.md
lark-cli api GET /open-apis/im/v1/messages/<om_…> --jq '.data.items[0].body.content'
```

The expansion only speaks in elements the post format has — `text` with a `style` list,
`a`, `at`, `emotion`, `img`, `code_block`, `hr`. A construct the format cannot name
arrives as text there whatever the client draws, so those rows say what reaches the
wire, not what a reader sees: inline code comes back as plain text in the expansion and
still draws as a code chip in the client. Where a row below turns on what a reader sees,
it was read off the client.

## What the expansion shows

| Written | On the wire |
|---|---|
| `**bold**` | `text` with `style:["bold"]` |
| `*italic*` | `text` with `style:["italic"]` |
| `~~strike~~` | `text` with `style:["lineThrough"]` |
| `https://example.com` bare | `a`, autolinked |
| `<at user_id="ou_a">林岚</at>` | `at` element, and the message gains a `mentions` entry |
| `@林岚` | plain text, and no `mentions` entry |
| `#` … `######` | the heading text, no marker |
| `> quote` | the text, no marker |
| `- item`, `1. item` | the marker survives as literal text; nesting is normalised to two spaces |
| `- [ ] task` | literal `[`, ` `, `] task` |
| A GFM table | one text run with the pipes in it, and the client draws a grid — see below |
| `` `code` `` | plain text |

A list broken by a blank line arrives as separate paragraphs, and an ordered list's
numbering carries across the break: `1.` then `2.` stays `1.` then `2.`.

## Links

A destination Feishu can read as an address becomes an `a` element with the href
written as-is — no scheme is added. Anything else is dropped and the label arrives as
plain text. The client draws the same split: the kept column reads as links, the
dropped column as ordinary words.

| Kept | Dropped |
|---|---|
| `https://example.com`, `http://example.com` | `/path` |
| `lark://applink.feishu.cn/client/chat/open?openChatId=oc_a` | `./notes.md`, `../up.md` |
| `www.example.com`, `example.com` | `#section` |
| `notes.md` — a bare name with a dot reads as a host | `$urlVal` |
| | `mailto:linlan@example.com` |

`lark://` surviving is what lets a post carry a jump back into the client.

## Tables

A pipe table with outer pipes draws as a real grid, and the cells carry their inline
content: `**bold**`, `` `code` `` and a link all render inside one. Seven body rows all
arrive — the five-row cap and the four-tables-per-component cap in the card
documentation are that renderer's, not this one's.

Two things are lost. Column alignment is dropped, every column reading left however the
delimiter row was written, and Feishu rewrites `| :--- | :----: | ----: |` to
`| --- | --- | --- |` on the way in. And a table written without outer pipes is not a
table: `- | -` opens a list, so the rows arrive as a stray bullet and some text with
pipes in it. goldmark reads that the same way, so a lint has nothing to add there — the
parser and Feishu agree, and only the author knows a table was meant.

Inside a table the expansion leaves `**bold**` and `[x](url)` as raw markdown, where
the same syntax outside one becomes a `style` or an `a`. The client renders what is in
the cells itself.

## Tags

`<at>` is the only tag a post parses. Every other one reaches the reader as the
characters it is spelled with, closing tag included, in the client as on the wire:
`<b>`, `<i>`, `<u>`, `<s>`, `<code>`, `<span>`, `<br>`, `<hr>`, `<div>`, and the card
renderer's own `<font>` and `<text_tag>`. There is no partial-HTML support here to
speak of — that belongs to the interactive card.

## Emoji

A text message draws an emoji from its written name, `[Name]`. A post draws one from an
`emotion` element only: the text inside an `md` element is kept as typed, brackets
included.

| Written | text message | inside an `md` element |
|---|---|---|
| `[Done]`, `[完成]`, `[Shrug]` | drawn as the emoji | the characters, brackets included |
| `:DONE:`, `:done:`, `:Shrug:` | the characters | the characters |
| `Done` bare | the characters | the characters |

Either language's name is accepted in a text message: `[Done]` and `[完成]` draw the same
emoji. Case in the colon form makes no difference because no colon form works anywhere —
`:KEY:` is how larkim writes an `emotion` element back out (`sync/post.go`), not a
spelling Feishu reads.

The client's editor turns a typed name into an `emotion` among the line's other
elements: `**abc** [Done] xyz` goes as

```json
[{"tag":"text","text":"abc","style":["bold"]},{"tag":"text","text":" ","style":[]},
 {"tag":"emotion","emoji_type":"Done"},{"tag":"text","text":" xyz","style":[]}]
```

`emoji_type` is read without regard to case: `Done` and `DONE` are stored as sent and
draw the same emoji. An `md` element always takes a line of its own, whatever shares its
paragraph: `[md "> abc", emotion]` is stored as two paragraphs, and
`[md "**abc** ", emotion, md " xyz"]` as three. A line that carries an emoji therefore
has to be spelled in those elements rather than cut out of an `md` one.

That is what the send does. A line of words — emphasis, strikethrough, `http(s)` links
and `<at>` mentions included — goes as `text`, `a`, `at` and `emotion` elements, each
`[Name]` in it becoming the emotion it names. Any other line — a heading, a quote, a list
item, a table, code, a picture — stays in its `md` element with the brackets in it. A
lazy line after a quote is a line of words, so `> abc` followed by `[Done]` sends the
quote and then the emoji on a line of its own.

`emojiInsert` writes `[Name]` for an emoji no character carries (`tui/pum.go`), so the
composer's popup draws the emoji in a text message and on a post's lines of words alike.
`emoji_not_rendered` in `larkmd` reports a name left inside an `md` line, and every
`:KEY:`.
