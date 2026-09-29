package cli

import (
	"fmt"
	"io"

	"github.com/amzyang/larkim/larkmd"
	"github.com/spf13/cobra"
)

func (a *App) lintCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "lint [file]",
		Short: "Read markdown the way a send will and report what Feishu loses",
		Long: "Read markdown the way `send --markdown` will and report what Feishu loses.\n\n" +
			"With no file, or with -, the body is read from stdin. Findings are advisory:\n" +
			"the same body sends fine, it just does not say what it looks like it says.",
		Args:              cobra.MaximumNArgs(1),
		ValidArgsFunction: completeLintSource,
		RunE: func(_ *cobra.Command, args []string) error {
			src := "-"
			if len(args) == 1 {
				src = args[0]
			}
			body, err := readSource(src, src, a.In)
			if err != nil {
				return &usageError{err}
			}
			return a.printFindings(larkmd.Lint(body))
		},
	}
	return cmd
}

func (a *App) printFindings(findings []larkmd.Finding) error {
	if a.json() {
		// An empty answer is an empty array rather than null: a reader that
		// ranges over it should not have to tell the two apart.
		if findings == nil {
			findings = []larkmd.Finding{}
		}
		return a.printJSON(findings)
	}
	writeFindings(a.Out, "", findings)
	if len(findings) == 0 {
		fmt.Fprintln(a.Out, "nothing to report")
	}
	return nil
}

// writeFindings prints what a body loses, a finding to the line with its hint
// under it. prefix names larkim on the lines a send writes to stderr beside
// its own output; the lint command, whose whole output this is, passes none.
func writeFindings(w io.Writer, prefix string, findings []larkmd.Finding) {
	for _, f := range findings {
		fmt.Fprintf(w, "%s%d:%d [%s] %s\n", prefix, f.Line, f.Column, f.Rule, f.Message)
		if f.Hint != "" {
			fmt.Fprintln(w, "  hint:", f.Hint)
		}
	}
}

// completeLintSource offers the one path the command takes. The default
// directive is what hands the listing back to the shell, which
// completeNoFileDefault would otherwise have turned off.
func completeLintSource(_ *cobra.Command, args []string, _ string) ([]cobra.Completion, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}
	return nil, cobra.ShellCompDirectiveDefault
}
