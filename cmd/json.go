package cmd

import (
	"fmt"

	"github.com/leezenn/slk/internal/format"
	"github.com/spf13/cobra"
)

func writeJSON(cmd *cobra.Command, payload map[string]interface{}) error {
	out, err := format.FormatJSON(payload)
	if err != nil {
		return internalError()
	}
	fmt.Fprintln(cmd.OutOrStdout(), out)
	return nil
}
