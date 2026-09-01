package cmd

import (
	"fmt"

	"github.com/leezenn/slk/internal/presentation"
	"github.com/spf13/cobra"
)

func addPresentationFlag(command *cobra.Command, target *string) {
	command.Flags().StringVar(
		target,
		"presentation",
		"",
		"Message presentation override: slack-managed or always-expanded",
	)
}

func resolvePresentation(command *cobra.Command, raw string, configured presentation.Mode) (presentation.Mode, error) {
	if command.Flags().Changed("presentation") {
		mode, known := presentation.Parse(raw)
		if !known {
			return "", invalidArgument(command, "--presentation must be slack-managed or always-expanded")
		}
		return mode, nil
	}
	mode, err := presentation.Effective(configured)
	if err != nil {
		return "", configLoadError(err)
	}
	return mode, nil
}

func presentationHelp(configured presentation.Mode) string {
	mode, err := presentation.Effective(configured)
	if err != nil {
		mode = presentation.Default()
	}
	return fmt.Sprintf(`

Message presentation:
  Effective default: %s
  --presentation overrides this command only.
  slack-managed leaves section collapsing to Slack.
  always-expanded asks Slack to keep generated sections expanded.`, mode)
}

func writeRequestedPresentation(command *cobra.Command, mode presentation.Mode) {
	fmt.Fprintf(command.OutOrStdout(), "Presentation requested: %s\n", mode)
}

func writePreservedPresentation(command *cobra.Command, mode presentation.Mode) {
	fmt.Fprintf(command.OutOrStdout(), "Presentation preserved: %s\n", mode)
}
