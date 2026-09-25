package tui

import (
	"testing"

	"github.com/amzyang/larkim/larkcli"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A lone link whose target is a file on this machine is an attachment, which
// is what a PDF copied in Finder was meant to be.
func TestPlanDraft_LoneLinkToALocalFileIsAFileMessage(t *testing.T) {
	f := fakeFiles(map[string]int64{"/Users/linlan/发布说明.pdf": 2048})

	p, err := f.planDraft("[发布说明.pdf](~/发布说明.pdf)")

	require.NoError(t, err)
	assert.Equal(t, kindFile, p.kind)
	assert.Equal(t, "file", p.kind.msgType())
	assert.Equal(t, "/Users/linlan/发布说明.pdf", p.file.local)
	assert.Equal(t, "[File: 发布说明.pdf]", p.body)
}

// The disk is what tells an attachment from a link, so an ordinary link in a
// message is untouched.
func TestPlanDraft_LinkToAURLStaysAPost(t *testing.T) {
	f := fakeFiles(nil)

	p, err := f.planDraft("[点这里](https://example.com/a.pdf)")

	require.NoError(t, err)
	assert.Equal(t, kindPost, p.kind)
	assert.Empty(t, p.file.local)
}

func TestPlanDraft_LinkToAMissingPathStaysAPost(t *testing.T) {
	f := fakeFiles(nil)

	p, err := f.planDraft("[没有](~/nowhere.pdf)")

	require.NoError(t, err)
	assert.Equal(t, kindPost, p.kind)
}

// Only a draft that is nothing but the link is an attachment; a link inside a
// sentence is a post about that link.
func TestPlanDraft_LinkWithTextAroundItStaysAPost(t *testing.T) {
	f := fakeFiles(map[string]int64{"/Users/linlan/发布说明.pdf": 2048})

	p, err := f.planDraft("看下 [发布说明.pdf](~/发布说明.pdf)")

	require.NoError(t, err)
	assert.Equal(t, kindPost, p.kind)
}

func TestPlanDraft_FileKeyFeishuAlreadyHoldsNeedsNoUpload(t *testing.T) {
	f := fakeFiles(nil)

	p, err := f.planDraft("[旧文件](file_v3_00abcdef)")

	require.NoError(t, err)
	assert.Equal(t, kindFile, p.kind)
	assert.Empty(t, p.file.local, "a key is already on Feishu")
	assert.Equal(t, "file_v3_00abcdef", p.send.FileKey)
}

// The reader meant to attach it, so the badge says why it will not go rather
// than quietly turning the draft back into a link.
func TestPlanDraft_FileOverTheLimitIsRefusedAsAFile(t *testing.T) {
	f := fakeFiles(map[string]int64{"/Users/linlan/big.zip": maxFileBytes + 1})

	p, err := f.planDraft("[big.zip](~/big.zip)")

	require.Error(t, err)
	assert.Equal(t, kindFile, p.kind)
	assert.Contains(t, err.Error(), "over the")
}

// A picture keeps the image path: it draws in the message list, which a file
// card does not.
func TestPlanDraft_ImageReferenceIsStillAnImage(t *testing.T) {
	f := fakeFiles(map[string]int64{"/Users/linlan/shot.png": 1024})

	p, err := f.planDraft("![](~/shot.png)")

	require.NoError(t, err)
	assert.Equal(t, kindImage, p.kind)
}

func TestDetail_NamesTheFileAndItsSize(t *testing.T) {
	f := fakeFiles(map[string]int64{"/Users/linlan/发布说明.pdf": 2048})
	p, err := f.planDraft("[发布说明.pdf](~/发布说明.pdf)")
	require.NoError(t, err)

	assert.Equal(t, "发布说明.pdf · 2.0 KB", p.detail())
}

// A path holding a space takes markdown's angle-bracket form, the only one
// that survives being read back.
func TestFileRef_WrapsAPathHoldingSpaces(t *testing.T) {
	assert.Equal(t, "[a.pdf](/tmp/a.pdf)", fileRef("/tmp/a.pdf"))
	assert.Equal(t, "[my report.pdf](</tmp/my report.pdf>)", fileRef("/tmp/my report.pdf"))
}

func TestSubmit_FileDraftUploadsThenSends(t *testing.T) {
	m, f := newOutboxModel(t)
	m.files = fakeFiles(map[string]int64{"/Users/linlan/Desktop/发布说明.pdf": 2048})
	m.input.SetValue("[发布说明.pdf](~/Desktop/发布说明.pdf)")

	mm, cmd := m.submit()
	m = mm.(Model)
	msg := cmd().(sentMsg)

	require.NoError(t, msg.err)
	require.Equal(t, []string{"/Users/linlan/Desktop/发布说明.pdf"}, f.Uploads)
	require.Len(t, f.Sent, 1)
	assert.Equal(t, "file_fake_1", f.Sent[0].FileKey)
	assert.Equal(t, []string{"file_fake_1"}, msg.keys)
	assert.Equal(t, "file", m.msgs[0].MsgType)
	assert.Equal(t, "[File: 发布说明.pdf]", m.msgs[0].Content)
}

// A retry is one more send, not one more upload leaving an orphan key behind.
func TestUploadDraft_RetryReusesTheKeyItAlreadyUploaded(t *testing.T) {
	m, f := newOutboxModel(t)
	file := draftFile{ref: "~/a.pdf", local: "/Users/linlan/a.pdf"}

	msg, keys, err := uploadDraft(m.deps, larkcli.Outgoing{}, nil, file, []string{"file_fake_1"})

	require.NoError(t, err)
	assert.Equal(t, "file_fake_1", msg.FileKey)
	assert.Equal(t, []string{"file_fake_1"}, keys)
	assert.Empty(t, f.Uploads, "nothing went up a second time")
}
