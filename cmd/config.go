package cmd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/leezenn/slk/internal/auth"
	"github.com/leezenn/slk/internal/config"
	"github.com/spf13/cobra"
)

const messagePrefixPreference = "message-prefix"

func newConfigCommand(deps Dependencies, rootOptions *rootOptions) *cobra.Command {
	command := &cobra.Command{
		Use:   "config",
		Short: "Inspect or change local slk configuration",
		Long: `Inspect effective slk preferences, manage command policy, or run guided setup.

Bare 'slk config' is read-only. Preference mutations are explicit subcommands.
The interactive setup journey reuses the existing verified Slack auth flow and
keeps existing credentials unless --reconnect is supplied.`,
		Args: argumentValidator(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runConfigSummary(cmd, deps, rootOptions)
		},
	}
	command.AddCommand(
		newConfigPathCommand(deps, rootOptions),
		newConfigSetCommand(deps, rootOptions),
		newConfigResetCommand(deps, rootOptions),
		newConfigDenyCommand(deps, rootOptions),
		newConfigAllowCommand(deps, rootOptions),
		newConfigSetupCommand(deps, rootOptions),
		newConfigDisconnectCommand(deps, rootOptions),
		newConfigDisableCommand(deps, rootOptions),
		newConfigEnableCommand(deps, rootOptions),
	)
	return command
}

func newConfigPathCommand(deps Dependencies, rootOptions *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "path",
		Short: "Print the stable configuration file path",
		Args:  argumentValidator(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			store, err := deps.configStore()
			if err != nil {
				return err
			}
			path, err := store.Path()
			if err != nil {
				return configLoadError(err)
			}
			if rootOptions.json {
				return writeJSON(cmd, map[string]interface{}{"ok": true, "path": path})
			}
			fmt.Fprintln(cmd.OutOrStdout(), path)
			return nil
		},
	}
}

func newConfigSetCommand(deps Dependencies, rootOptions *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "set message-prefix <text>",
		Short: "Set one configuration preference",
		Long: `Set the message prefix rendered before new and replaced messages.

An explicitly empty text value disables the prefix. Use
'slk config reset message-prefix' to return to the built-in default.`,
		Args: argumentValidator(cobra.ExactArgs(2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if args[0] != messagePrefixPreference {
				return invalidArgument(cmd, "unknown preference "+args[0])
			}
			document, store, path, err := loadConfigDocument(deps)
			if err != nil {
				return err
			}
			prefix := args[1]
			document.MessagePrefix = &prefix
			if err := saveConfigDocument(store, document); err != nil {
				return err
			}
			return writeConfigReceipt(cmd, rootOptions, "message prefix updated", path, document.Effective())
		},
	}
}

func newConfigResetCommand(deps Dependencies, rootOptions *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "reset message-prefix",
		Short: "Reset one preference to its built-in default",
		Args:  argumentValidator(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			if args[0] != messagePrefixPreference {
				return invalidArgument(cmd, "unknown preference "+args[0])
			}
			document, store, path, err := loadConfigDocument(deps)
			if err != nil {
				return err
			}
			document.MessagePrefix = nil
			if err := saveConfigDocument(store, document); err != nil {
				return err
			}
			return writeConfigReceipt(cmd, rootOptions, "message prefix reset", path, document.Effective())
		},
	}
}

func newConfigDenyCommand(deps Dependencies, rootOptions *rootOptions) *cobra.Command {
	return newMutationPolicyCommand(deps, rootOptions, true)
}

func newConfigAllowCommand(deps Dependencies, rootOptions *rootOptions) *cobra.Command {
	return newMutationPolicyCommand(deps, rootOptions, false)
}

func newMutationPolicyCommand(deps Dependencies, rootOptions *rootOptions, deny bool) *cobra.Command {
	verb := "allow"
	short := "Allow one Slack mutation command"
	if deny {
		verb = "deny"
		short = "Deny one Slack mutation command"
	}
	return &cobra.Command{
		Use:   verb + " <delete|edit|replace|reply|write>",
		Short: short,
		Args:  argumentValidator(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			mutation, known := config.ParseMutation(args[0])
			if !known {
				return invalidArgument(cmd, "unknown mutation command "+args[0])
			}
			document, store, path, err := loadConfigDocument(deps)
			if err != nil {
				return err
			}
			setMutationDenied(&document, mutation, deny)
			if err := saveConfigDocument(store, document); err != nil {
				return err
			}
			action := fmt.Sprintf("%s allowed", mutation)
			if deny {
				action = fmt.Sprintf("%s denied", mutation)
			}
			return writeConfigReceipt(cmd, rootOptions, action, path, document.Effective())
		},
	}
}

func newConfigDisconnectCommand(deps Dependencies, rootOptions *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "disconnect",
		Short: "Remove the locally stored Slack credential",
		Long: `Remove the credential stored by slk on this machine.

This does not revoke the OAuth token at Slack and does not disable slk. If
SLACK_TOKEN is set, Slack operations may remain authenticated until the tool is
explicitly disabled or that environment variable is removed.`,
		Args: argumentValidator(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			credentials, err := deps.credentialStore()
			if err != nil {
				return err
			}
			if err := credentials.Clear(); err != nil {
				return credentialBackendError(err)
			}
			environmentActive := false
			if result, err := credentials.Get(); err == nil && result.Source == auth.SourceEnv {
				environmentActive = true
			}
			if rootOptions.json {
				return writeJSON(cmd, map[string]interface{}{
					"ok":                      true,
					"disconnected":            true,
					"environment_auth_active": environmentActive,
				})
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Stored Slack credential removed from %s.\n", credStoreName())
			if environmentActive {
				fmt.Fprintln(cmd.OutOrStdout(), "SLACK_TOKEN remains active. Run 'slk config disable' to block Slack operations or remove the environment variable.")
			}
			return nil
		},
	}
}

func newConfigDisableCommand(deps Dependencies, rootOptions *rootOptions) *cobra.Command {
	return newToolStateCommand(deps, rootOptions, true)
}

func newConfigEnableCommand(deps Dependencies, rootOptions *rootOptions) *cobra.Command {
	return newToolStateCommand(deps, rootOptions, false)
}

func newToolStateCommand(deps Dependencies, rootOptions *rootOptions, disable bool) *cobra.Command {
	name := "enable"
	short := "Enable Slack operational commands"
	if disable {
		name = "disable"
		short = "Disable every Slack operational command"
	}
	return &cobra.Command{
		Use:   name,
		Short: short,
		Long: fmt.Sprintf(`%s slk operational commands through local configuration.

Environment and stored credentials are ignored while disabled. Agents must ask
the user for permission before running 'slk config enable'.`, strings.ToUpper(name[:1])+name[1:]),
		Args: argumentValidator(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			document, store, path, err := loadConfigDocument(deps)
			if err != nil {
				return err
			}
			document.Disabled = disable
			if err := saveConfigDocument(store, document); err != nil {
				return err
			}
			action := "slk enabled"
			if disable {
				action = "slk disabled"
			}
			return writeConfigReceipt(cmd, rootOptions, action, path, document.Effective())
		},
	}
}

func runConfigSummary(cmd *cobra.Command, deps Dependencies, rootOptions *rootOptions) error {
	document, _, path, err := loadConfigDocument(deps)
	if err != nil {
		return err
	}
	settings := document.Effective()
	credentials, err := deps.credentialStore()
	if err != nil {
		return err
	}
	authResult, authErr := credentials.Get()
	configured := authErr == nil

	if rootOptions.json {
		payload := map[string]interface{}{
			"ok":                    true,
			"path":                  path,
			"disabled":              settings.Disabled,
			"message_prefix":        settings.MessagePrefix,
			"message_prefix_source": prefixSource(document),
			"deny_mutations":        mutationStrings(settings.DeniedMutations),
			"auth_configured":       configured,
			"auth_ignored":          configured && settings.Disabled,
		}
		if configured {
			payload["auth_source"] = authResult.Source
		}
		return writeJSON(cmd, payload)
	}

	fmt.Fprintf(cmd.OutOrStdout(), "Path: %s\n", path)
	if settings.Disabled {
		fmt.Fprintln(cmd.OutOrStdout(), "Tool: disabled")
	} else {
		fmt.Fprintln(cmd.OutOrStdout(), "Tool: enabled")
	}
	if configured {
		fmt.Fprintf(cmd.OutOrStdout(), "Authentication: configured (%s, %s)\n", authResult.Source, auth.MaskToken(authResult.Token))
		if settings.Disabled {
			fmt.Fprintln(cmd.OutOrStdout(), "Authentication use: ignored while slk is disabled")
		}
	} else {
		fmt.Fprintln(cmd.OutOrStdout(), "Authentication: not configured")
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Message prefix (%s): %q\n", prefixSource(document), settings.MessagePrefix)
	fmt.Fprintf(cmd.OutOrStdout(), "Denied mutations: %s\n", mutationList(settings.DeniedMutations))
	if settings.Disabled {
		fmt.Fprintln(cmd.OutOrStdout(), "Agent guidance: ask the user for permission before running 'slk config enable'.")
	}
	return nil
}

func loadConfigDocument(deps Dependencies) (config.Document, config.Store, string, error) {
	store, err := deps.configStore()
	if err != nil {
		return config.Document{}, nil, "", err
	}
	path, err := store.Path()
	if err != nil {
		return config.Document{}, nil, "", configLoadError(err)
	}
	document, err := store.LoadDocument()
	if err != nil {
		return config.Document{}, nil, "", configLoadError(err)
	}
	return document, store, path, nil
}

func saveConfigDocument(store config.Store, document config.Document) error {
	if err := store.Save(document); err != nil {
		return configSaveError(err)
	}
	return nil
}

func setMutationDenied(document *config.Document, mutation config.Mutation, denied bool) {
	filtered := document.DeniedMutations[:0]
	for _, existing := range document.DeniedMutations {
		if existing != mutation {
			filtered = append(filtered, existing)
		}
	}
	document.DeniedMutations = filtered
	if denied {
		document.DeniedMutations = append(document.DeniedMutations, mutation)
	}
}

func writeConfigReceipt(cmd *cobra.Command, rootOptions *rootOptions, action, path string, settings config.Settings) error {
	if rootOptions.json {
		return writeJSON(cmd, map[string]interface{}{
			"ok":             true,
			"action":         action,
			"path":           path,
			"disabled":       settings.Disabled,
			"message_prefix": settings.MessagePrefix,
			"deny_mutations": mutationStrings(settings.DeniedMutations),
		})
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s.\n", strings.ToUpper(action[:1])+action[1:])
	fmt.Fprintf(cmd.OutOrStdout(), "Configuration: %s\n", path)
	return nil
}

func prefixSource(document config.Document) string {
	if document.MessagePrefix == nil {
		return "default"
	}
	if *document.MessagePrefix == "" {
		return "disabled"
	}
	return "custom"
}

func mutationStrings(mutations []config.Mutation) []string {
	values := make([]string, len(mutations))
	for index, mutation := range mutations {
		values[index] = string(mutation)
	}
	sort.Strings(values)
	return values
}

func mutationList(mutations []config.Mutation) string {
	values := mutationStrings(mutations)
	if len(values) == 0 {
		return "none"
	}
	return strings.Join(values, ", ")
}
