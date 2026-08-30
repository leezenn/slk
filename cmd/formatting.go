package cmd

import (
	"fmt"

	"github.com/leezenn/slk/internal/textformat"
	"github.com/spf13/cobra"
)

func formattingHelp(modules []textformat.Module, submittedText string) string {
	if len(modules) == 0 {
		return fmt.Sprintf(`

Formatting: disabled. %s remains exact.
Inspect or enable modules with 'slk config formatting'.`, submittedText)
	}
	return fmt.Sprintf(`

Formatting enabled: %s.
%s is transformed before Slack receives it. The em-dash-to-spaced-hyphen
module changes word—word and word — word into word - word. Mutation receipts
report formatting_applied. Change modules with 'slk config formatting'.`, textformat.List(modules), submittedText)
}

func rootFormattingHelp(modules []textformat.Module) string {
	if len(modules) == 0 {
		return `

Formatting:
  Enabled modules: none (submitted mutation text remains exact)`
	}
	return fmt.Sprintf(`

Formatting:
  Enabled modules: %s
  Submitted mutation text may change before Slack receives it; receipts report
  formatting_applied.`, textformat.List(modules))
}

func formattingReceipt(applied []textformat.Module) []string {
	return textformat.Names(applied)
}

func writeFormattingNotice(cmd *cobra.Command, applied []textformat.Module) {
	if len(applied) == 0 {
		return
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Formatting applied: %s.\n", textformat.List(applied))
}
