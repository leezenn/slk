package cmd

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	jsonOutput bool
	version    = "dev" // set by -ldflags at build time
)

var rootCmd = &cobra.Command{
	Version: version,
	Use:   "slk",
	Short: "Read Slack channels, DMs, threads, and files from the command line",
	Long: `Read Slack channels, DMs, threads, and files from the command line.

Environment:
  SLACK_TOKEN  Fallback token if keychain is not configured`,
	Example: `  slk auth xoxp-your-token-here
  slk channels --type dm
  slk read general --limit 50
  slk read @john --after 1d
  slk thread general 1705312325.000100
  slk search "deploy failed"
  slk download https://files.slack.com/... -o report.pdf`,
}

func init() {
	rootCmd.PersistentFlags().BoolVar(&jsonOutput, "json", false, "Output as JSON")
}

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
