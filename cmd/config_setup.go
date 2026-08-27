package cmd

import (
	"fmt"
	"strings"

	"github.com/leezenn/slk/internal/auth"
	"github.com/leezenn/slk/internal/config"
	"github.com/spf13/cobra"
)

type configSetupOptions struct {
	reconnect bool
}

func newConfigSetupCommand(deps Dependencies, rootOptions *rootOptions) *cobra.Command {
	options := &configSetupOptions{}
	command := &cobra.Command{
		Use:   "setup",
		Short: "Run guided human setup for auth and preferences",
		Long: `Run a review-first interactive setup journey.

Existing authentication is kept by default. Use --reconnect to validate and
replace it through the existing Slack auth flow. Setup never silently enables a
disabled tool; use 'slk config enable' separately after explicit approval.`,
		Args: argumentValidator(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if rootOptions.json {
				return conflictingOptions(cmd, "interactive setup does not support --json")
			}
			return runConfigSetup(cmd, deps, options.reconnect)
		},
	}
	command.Flags().BoolVar(&options.reconnect, "reconnect", false, "Validate and replace an existing Slack credential")
	return command
}

func runConfigSetup(cmd *cobra.Command, deps Dependencies, reconnect bool) error {
	document, store, path, err := loadConfigDocument(deps)
	if err != nil {
		return err
	}
	credentials, err := deps.credentialStore()
	if err != nil {
		return err
	}
	reader := newCommandLineReader(cmd)
	result, authErr := credentials.Get()
	if reconnect || authErr != nil {
		if err := guidedSetupWithReader(cmd, deps, credentials, reconnect, reader); err != nil {
			return err
		}
		result, authErr = credentials.Get()
	} else {
		fmt.Fprintf(cmd.OutOrStdout(), "Authentication: configured (%s, %s)\n", result.Source, auth.MaskToken(result.Token))
	}
	if authErr != nil {
		return authRequiredError()
	}

	settings := document.Effective()
	fmt.Fprintf(cmd.OutOrStdout(), "Configuration: %s\n", path)
	if settings.Disabled {
		fmt.Fprintln(cmd.OutOrStdout(), "Tool: disabled (setup will not enable it)")
	} else {
		fmt.Fprintln(cmd.OutOrStdout(), "Tool: enabled")
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Message prefix (%s): %q\n", prefixSource(document), settings.MessagePrefix)
	fmt.Fprintf(cmd.OutOrStdout(), "Denied mutations: %s\n", mutationList(settings.DeniedMutations))

	change, err := promptYesNo(cmd, reader, "Change preferences?", false)
	if err != nil {
		return err
	}
	if !change {
		fmt.Fprintln(cmd.OutOrStdout(), "Preferences unchanged.")
		return setupDisabledReminder(cmd, settings.Disabled)
	}

	changePrefix, err := promptYesNo(cmd, reader, "Change the message prefix?", false)
	if err != nil {
		return err
	}
	if changePrefix {
		fmt.Fprint(cmd.OutOrStdout(), "New message prefix (empty disables it): ")
		prefix, err := reader.ReadLine()
		if err != nil {
			return err
		}
		document.MessagePrefix = &prefix
	}

	allowReply, err := promptYesNo(cmd, reader, "Allow thread replies?", !settings.MutationDenied(config.MutationReply))
	if err != nil {
		return err
	}
	allowWrite, err := promptYesNo(cmd, reader, "Allow top-level writes?", !settings.MutationDenied(config.MutationWrite))
	if err != nil {
		return err
	}
	allowReplace, err := promptYesNo(cmd, reader, "Allow complete message replacement?", !settings.MutationDenied(config.MutationReplace))
	if err != nil {
		return err
	}
	allowDelete, err := promptYesNo(cmd, reader, "Allow permanent message deletion?", !settings.MutationDenied(config.MutationDelete))
	if err != nil {
		return err
	}
	setMutationDenied(&document, config.MutationReply, !allowReply)
	setMutationDenied(&document, config.MutationWrite, !allowWrite)
	setMutationDenied(&document, config.MutationReplace, !allowReplace)
	setMutationDenied(&document, config.MutationDelete, !allowDelete)
	if err := saveConfigDocument(store, document); err != nil {
		return err
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Preferences saved to %s.\n", path)
	return setupDisabledReminder(cmd, document.Disabled)
}

func promptYesNo(cmd *cobra.Command, reader *commandLineReader, label string, defaultYes bool) (bool, error) {
	suffix := "[y/N]"
	if defaultYes {
		suffix = "[Y/n]"
	}
	for {
		fmt.Fprintf(cmd.OutOrStdout(), "%s %s ", label, suffix)
		line, err := reader.ReadLine()
		if err != nil {
			return false, err
		}
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "":
			return defaultYes, nil
		case "y", "yes":
			return true, nil
		case "n", "no":
			return false, nil
		default:
			fmt.Fprintln(cmd.OutOrStdout(), "Please answer yes or no.")
		}
	}
}

func setupDisabledReminder(cmd *cobra.Command, disabled bool) error {
	if disabled {
		fmt.Fprintln(cmd.OutOrStdout(), "slk remains disabled. Ask the user for permission before running 'slk config enable'.")
	}
	return nil
}
