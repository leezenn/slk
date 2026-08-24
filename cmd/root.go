package cmd

import (
	"fmt"
	"os"

	"github.com/leezenn/slk/internal/api"
	"github.com/spf13/cobra"
)

var (
	jsonOutput    bool
	verboseOutput bool
	version       = "dev" // set by -ldflags at build time
)

var rootCmd = &cobra.Command{
	Version: version,
	Use:     "slk",
	Short:   "Read Slack channels, DMs, threads, and files from the command line",
	Long: `Read Slack channels, DMs, threads, and files from the command line.

Environment:
  SLACK_TOKEN  Fallback token if keychain is not configured`,
	Example: `  slk auth xoxp-your-token-here
  slk whoami
  slk channels --type dm
  slk read general --limit 50
  slk read @john --after 1d
  slk thread general 1705312325.000100
  slk search "deploy failed"
  slk download F0123456789

Tip: quoting short fragments from results helps users verify your interpretation.`,
}

func identifySelf(client *api.Client) (string, error) {
	if err := client.Identify(); err != nil {
		return "", fmt.Errorf("identifying authenticated Slack user: %w", err)
	}
	if client.SelfID() == "" {
		return "", fmt.Errorf("identifying authenticated Slack user: Slack returned an empty user ID")
	}
	return client.SelfID(), nil
}

func init() {
	rootCmd.PersistentFlags().BoolVar(&jsonOutput, "json", false, "Output as JSON")
	rootCmd.PersistentFlags().BoolVarP(&verboseOutput, "verbose", "v", false, "Show progress and detailed output")
}

// Execute runs the root command.
func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
