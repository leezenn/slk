package cmd

import (
	"fmt"

	"github.com/leezenn/slk/internal/textformat"
	"github.com/spf13/cobra"
)

func formattingHelp(submittedText string) string {
	return fmt.Sprintf(`

Formatting:
  Built-in default: disabled.
  Authenticated identity preferences are applied at execution.
  When enabled, %s may be transformed before Slack receives it; mutation
  receipts report formatting_applied. Inspect modules with 'slk config formatting'.`, submittedText)
}

func rootFormattingHelp() string {
	return `

Formatting:
  Built-in default: disabled.
  Authenticated identity preferences are applied at execution; mutation
  receipts report formatting_applied.`
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
