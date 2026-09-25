package tui

import (
	"fmt"
	"os"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// editedMsg answers the external editor: path is the file it was handed, err
// whatever went wrong running it.
type editedMsg struct {
	path string
	err  error
}

// editorFor resolves the editor the way every terminal tool does. env is
// injected so a test never launches one.
func editorFor(env func(string) string) []string {
	for _, k := range []string{"VISUAL", "EDITOR"} {
		if v := strings.Fields(env(k)); len(v) > 0 {
			return v
		}
	}
	return []string{"vi"}
}

// editExternally hands the draft to $VISUAL or $EDITOR. The file is named .md
// because that is what makes the editor highlight it as markdown, which is
// the point of going out to one at all.
func editExternally(env func(string) string, draft string) tea.Cmd {
	f, err := os.CreateTemp("", "larkim-*.md")
	if err != nil {
		return func() tea.Msg { return editedMsg{err: fmt.Errorf("editor: %w", err)} }
	}
	path := f.Name()
	_, werr := f.WriteString(draft)
	cerr := f.Close()
	if err := firstErr(werr, cerr); err != nil {
		os.Remove(path)
		return func() tea.Msg { return editedMsg{err: fmt.Errorf("editor: %w", err)} }
	}
	argv := append(editorFor(env), path)
	return tea.ExecProcess(exec.Command(argv[0], argv[1:]...), func(err error) tea.Msg {
		return editedMsg{path: path, err: err}
	})
}

func firstErr(errs ...error) error {
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}
