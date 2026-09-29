# What a post `md` element does with markdown

`larkim send --markdown` puts a body inside `{"tag":"md","text":…}` post elements
(`larkcli/exec.go`, `postContent`). This records what Feishu does with what is inside
one, so a lint rule cites an observation rather than the interactive-card documentation,
which describes a different renderer.

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

Feishu takes one written spelling of an emoji, `[Name]`, and it works in a plain text
message only.

| Written | text message | post `--markdown` |
|---|---|---|
| `[Done]`, `[完成]`, `[Shrug]` | drawn as the emoji | the characters, brackets included |
| `:DONE:`, `:done:`, `:Shrug:` | the characters | the characters |
| `Done` bare | the characters | the characters |

Either language's name is accepted in a text message: `[Done]` and `[完成]` draw the same
emoji. Case in the colon form makes no difference because no colon form works anywhere —
`:KEY:` is how larkim writes an `emotion` element back out (`sync/post.go`), not a
spelling Feishu reads.

A post draws an emoji from an `emotion` element, which is what a client emits when one
is picked from its panel. A body sent through `--markdown` is one `md` element with no
room for an `emotion` beside it, so the only emoji such a body can carry is a character:
🤷 for one Unicode has, nothing for Feishu's own art.

This is what the composer's popup runs into. `emojiInsert` writes `[Name]` for an emoji
no character carries (`tui/pum.go`), so the same keystrokes draw an emoji while the
draft is text and a pair of brackets once something else in the draft turns it into a
post. `emoji_not_rendered` in `larkmd` reports both spellings.
