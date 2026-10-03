package tui

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// larkHTML wraps body in the root div the Lark client writes.
func larkHTML(body string) []byte {
	return []byte(`<meta charset='utf-8'><div data-lark-html-role="root"><div class="richTextDocs">` +
		body + `</div></div>`)
}

func TestLarkPaste_PlainTextParagraph(t *testing.T) {
	t.Parallel()
	md, ok := larkPaste(larkHTML(
		`<div class="rich-text-paragraph"><span class="text-only">hello world</span></div>`))
	require.True(t, ok)
	assert.Equal(t, "hello world", md)
}

func TestLarkPaste_TwoParagraphs(t *testing.T) {
	t.Parallel()
	md, ok := larkPaste(larkHTML(
		`<div class="rich-text-paragraph"><span>first</span></div>` +
			`<div class="rich-text-paragraph"><span>second</span></div>`))
	require.True(t, ok)
	assert.Equal(t, "first\nsecond", md)
}

func TestLarkPaste_BlankLineBetweenParagraphs(t *testing.T) {
	t.Parallel()
	md, ok := larkPaste(larkHTML(
		`<div class="rich-text-paragraph"><span>above</span></div>` +
			`<div class="rich-text-paragraph"></div>` +
			`<div class="rich-text-paragraph"><span>below</span></div>`))
	require.True(t, ok)
	assert.Equal(t, "above\n\nbelow", md)
}

func TestLarkPaste_LinkBareURL(t *testing.T) {
	t.Parallel()
	md, ok := larkPaste(larkHTML(
		`<div class="rich-text-paragraph">` +
			`<span>see </span>` +
			`<a href="https://example.com/">https://example.com</a>` +
			`<span> here</span></div>`))
	require.True(t, ok)
	assert.Equal(t, "see https://example.com/ here", md)
}

func TestLarkPaste_LinkWithLabel(t *testing.T) {
	t.Parallel()
	md, ok := larkPaste(larkHTML(
		`<div class="rich-text-paragraph">` +
			`<a href="https://example.com/page">看板</a></div>`))
	require.True(t, ok)
	assert.Equal(t, "[看板](https://example.com/page)", md)
}

func TestLarkPaste_ImageWithOriginFile(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	img := filepath.Join(dir, "pic.jpg")
	require.NoError(t, os.WriteFile(img, []byte("fake"), 0o644))

	md, ok := larkPaste(larkHTML(
		`<figure class="rich-text-image"><div><img ` +
			`data-origin-file="` + img + `" ` +
			`data-image-key="img_v3_abc_MIDDLE_WEBP"></div></figure>`))
	require.True(t, ok)
	assert.Equal(t, imageRef(img), md)
}

func TestLarkPaste_ImageFallsBackToKey(t *testing.T) {
	t.Parallel()
	md, ok := larkPaste(larkHTML(
		`<figure class="rich-text-image"><div><img ` +
			`data-origin-file="/no/such/file.jpg" ` +
			`data-image-key="img_v3_abc_MIDDLE_WEBP"></div></figure>`))
	require.True(t, ok)
	assert.Equal(t, "![](img_v3_abc)", md, "key suffix stripped, used as fallback")
}

func TestLarkPaste_ImageKeyWithoutOrigin(t *testing.T) {
	t.Parallel()
	md, ok := larkPaste(larkHTML(
		`<figure class="rich-text-image"><div><img ` +
			`data-image-key="img_v3_xyz_NOOP_WEBP"></div></figure>`))
	require.True(t, ok)
	assert.Equal(t, "![](img_v3_xyz)", md)
}

func TestLarkPaste_MixedTextLinkImage(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	img := filepath.Join(dir, "shot.png")
	require.NoError(t, os.WriteFile(img, []byte("png"), 0o644))

	md, ok := larkPaste(larkHTML(
		`<div class="rich-text-paragraph">` +
			`<span>ab </span>` +
			`<a href="https://www.baidu.com/" data-lark-link="true">https://www.baidu.com</a>` +
			`<span> bc</span></div>` +
			`<div class="rich-text-paragraph"></div>` +
			`<figure class="rich-text-image"><div><img ` +
			`data-origin-file="` + img + `" ` +
			`data-image-key="img_v3_k_MIDDLE_WEBP"></div></figure>` +
			`<div class="rich-text-paragraph"><span> xyz</span></div>`))
	require.True(t, ok)
	want := "ab https://www.baidu.com/ bc\n\n" + imageRef(img) + "\n xyz"
	assert.Equal(t, want, md)
}

func TestLarkPaste_Mention(t *testing.T) {
	t.Parallel()
	md, ok := larkPaste(larkHTML(
		`<div class="rich-text-paragraph">` +
			`<span class="rich-text-at" data-at-id="ou_abc">@张三</span>` +
			`<span> 看下</span></div>`))
	require.True(t, ok)
	assert.Equal(t, "@张三 看下", md)
}

func TestLarkPaste_Bold(t *testing.T) {
	t.Parallel()
	md, ok := larkPaste(larkHTML(
		`<div class="rich-text-paragraph"><b>重点</b></div>`))
	require.True(t, ok)
	assert.Equal(t, "**重点**", md)
}

func TestLarkPaste_Strikethrough(t *testing.T) {
	t.Parallel()
	md, ok := larkPaste(larkHTML(
		`<div class="rich-text-paragraph"><del>删掉</del></div>`))
	require.True(t, ok)
	assert.Equal(t, "~~删掉~~", md)
}

func TestLarkPaste_InlineCode(t *testing.T) {
	t.Parallel()
	md, ok := larkPaste(larkHTML(
		`<div class="rich-text-paragraph"><code>foo()</code></div>`))
	require.True(t, ok)
	assert.Equal(t, "`foo()`", md)
}

func TestLarkPaste_CodeBlock(t *testing.T) {
	t.Parallel()
	md, ok := larkPaste(larkHTML(
		`<div class="rich-text-code-block" data-language="go">func main() {}<br>return</div>`))
	require.True(t, ok)
	assert.Equal(t, "```go\nfunc main() {}\nreturn\n```", md)
}

func TestLarkPaste_OrderedList(t *testing.T) {
	t.Parallel()
	md, ok := larkPaste(larkHTML(
		`<div class="rich-text-ordered-list"><span>first</span></div>` +
			`<div class="rich-text-ordered-list"><span>second</span></div>`))
	require.True(t, ok)
	assert.Equal(t, "1. first\n1. second", md)
}

func TestLarkPaste_UnorderedList(t *testing.T) {
	t.Parallel()
	md, ok := larkPaste(larkHTML(
		`<div class="rich-text-unordered-list"><span>alpha</span></div>` +
			`<div class="rich-text-unordered-list"><span>beta</span></div>`))
	require.True(t, ok)
	assert.Equal(t, "- alpha\n- beta", md)
}

func TestLarkPaste_HTMLListsKeepTheirItemsApart(t *testing.T) {
	t.Parallel()
	md, ok := larkPaste(larkHTML(`<ol><li>first</li><li>second</li></ol><ul><li>alpha</li></ul>`))
	require.True(t, ok)
	assert.Equal(t, "1. first\n1. second\n- alpha", md)
}

func TestLarkPaste_HTMLHeadingsAndParagraphsKeepTheirLines(t *testing.T) {
	t.Parallel()
	md, ok := larkPaste(larkHTML(`<h2>Plan</h2><p>First line.</p><p>Second para.</p>`))
	require.True(t, ok)
	assert.Equal(t, "## Plan\nFirst line.\nSecond para.", md)
}

func TestLarkPaste_HTMLPreIsAFencedBlock(t *testing.T) {
	t.Parallel()
	md, ok := larkPaste(larkHTML("<p>before</p><pre><code data-lark-language=\"go\">a := 1\nb := 2</code></pre>"))
	require.True(t, ok)
	assert.Equal(t, "before\n```go\na := 1\nb := 2\n```", md)
}

func TestLarkPaste_Blockquote(t *testing.T) {
	t.Parallel()
	md, ok := larkPaste(larkHTML(
		`<div class="rich-text-quote"><span>quoted text</span></div>`))
	require.True(t, ok)
	assert.Equal(t, "> quoted text", md)
}

func TestLarkPaste_NativeBlockquoteWrappingParagraph(t *testing.T) {
	t.Parallel()
	md, ok := larkPaste(larkHTML(
		`<blockquote><div class="rich-text-paragraph"><span class="text-only">abc</span></div></blockquote>`))
	require.True(t, ok)
	assert.Equal(t, "> abc", md)
}

func TestLarkPaste_EmojiSpan(t *testing.T) {
	t.Parallel()
	md, ok := larkPaste(larkHTML(
		`<div class="rich-text-paragraph">` +
			`<span class="larkw-emoji__wrapper"><span class="larkw-emoji__copy">[Done]</span></span></div>`))
	require.True(t, ok)
	assert.Equal(t, "[Done]", md)
}

func TestLarkPaste_NonLarkHTMLReturnsFalse(t *testing.T) {
	t.Parallel()
	_, ok := larkPaste([]byte(`<html><body><p>plain web page</p></body></html>`))
	assert.False(t, ok)
}

func TestStripImageKeySuffix(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ in, want string }{
		{"img_v3_abc_MIDDLE_WEBP", "img_v3_abc"},
		{"img_v3_abc_NOOP_WEBP", "img_v3_abc"},
		{"img_v3_abc_ORIGIN_WEBP", "img_v3_abc"},
		{"img_v3_abc_MIDDLE_PNG", "img_v3_abc"},
		{"img_v3_abc", "img_v3_abc"},
		{"", ""},
	} {
		assert.Equal(t, tc.want, stripImageKeySuffix(tc.in), tc.in)
	}
}

func TestURLsEquivalent(t *testing.T) {
	t.Parallel()
	assert.True(t, urlsEquivalent("https://example.com", "https://example.com/"))
	assert.True(t, urlsEquivalent("https://example.com/", "https://example.com/"))
	assert.False(t, urlsEquivalent("https://example.com/a", "https://example.com/b"))
}
