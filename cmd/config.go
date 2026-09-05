package cmd

import (
	"fmt"
	"sort"
	"strings"

	"github.com/leezenn/slk/internal/auth"
	"github.com/leezenn/slk/internal/config"
	"github.com/leezenn/slk/internal/presentation"
	"github.com/leezenn/slk/internal/textformat"
	"github.com/spf13/cobra"
)

const (
	messagePrefixPreference       = "message-prefix"
	messagePresentationPreference = "message-presentation"
)

func newConfigCommand(deps Dependencies, rootOptions *rootOptions) *cobra.Command {
	command := &cobra.Command{
		Use:   "config",
		Short: "Inspect or change local slk configuration",
		Long: `Inspect effective slk preferences, manage command policy, or run guided setup.

Bare 'slk config' is read-only except for the one-time upgrade of released
flat preferences into the validated identity entry. Preference mutations are
explicit subcommands. Guided setup keeps existing credentials unless
--reconnect is supplied.`,
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
		newConfigFormattingCommand(deps, rootOptions),
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
			writeConfigLocation(cmd, path)
			return nil
		},
	}
}

func newConfigSetCommand(deps Dependencies, rootOptions *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "set <message-prefix|message-presentation> <value>",
		Short: "Set one configuration preference",
		Long: `Set one message-expression preference.

An explicitly empty message-prefix value disables the prefix. The
message-presentation value must be slack-managed or always-expanded. Use
'slk config reset <preference>' to return that preference to its built-in default.`,
		Args: argumentValidator(cobra.ExactArgs(2)),
		RunE: func(cmd *cobra.Command, args []string) error {
			preference := args[0]
			var mode presentation.Mode
			switch preference {
			case messagePrefixPreference:
			case messagePresentationPreference:
				var known bool
				mode, known = presentation.Parse(args[1])
				if !known {
					return invalidArgument(cmd, "message-presentation must be slack-managed or always-expanded")
				}
			default:
				return invalidArgument(cmd, "unknown preference "+preference)
			}
			if err := requireIdentityPreferencesEnabled(deps); err != nil {
				return err
			}

			bound, document, preferences, store, path, err := loadRequiredIdentityConfig(cmd, deps)
			if err != nil {
				return err
			}
			action := "message prefix updated"
			if preference == messagePrefixPreference {
				prefix := args[1]
				preferences.MessagePrefix = &prefix
			} else {
				preferences.MessagePresentation = &mode
				action = "message presentation updated"
			}
			if err := saveIdentityConfigDocument(bound, store, document, preferences); err != nil {
				return err
			}
			return writeIdentityConfigReceipt(cmd, rootOptions, action, path, config.Merge(document, preferences))
		},
	}
}

func newConfigResetCommand(deps Dependencies, rootOptions *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "reset <message-prefix|message-presentation>",
		Short: "Reset one preference to its built-in default",
		Args:  argumentValidator(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			preference := args[0]
			if preference != messagePrefixPreference && preference != messagePresentationPreference {
				return invalidArgument(cmd, "unknown preference "+preference)
			}
			if err := requireIdentityPreferencesEnabled(deps); err != nil {
				return err
			}
			bound, document, preferences, store, path, err := loadRequiredIdentityConfig(cmd, deps)
			if err != nil {
				return err
			}
			action := "message prefix reset"
			if preference == messagePrefixPreference {
				preferences.MessagePrefix = nil
			} else {
				preferences.MessagePresentation = nil
				action = "message presentation reset"
			}
			if err := saveIdentityConfigDocument(bound, store, document, preferences); err != nil {
				return err
			}
			return writeIdentityConfigReceipt(cmd, rootOptions, action, path, config.Merge(document, preferences))
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

func newConfigFormattingCommand(deps Dependencies, rootOptions *rootOptions) *cobra.Command {
	command := &cobra.Command{
		Use:   "formatting",
		Short: "Inspect or change opt-in text formatting",
		Long: `Inspect or explicitly enable named formatting modules.

Formatting is disabled by default so submitted model text remains exact. The
em-dash-to-spaced-hyphen module changes surrounding horizontal whitespace plus
an em dash into one spaced ASCII hyphen. It applies only to submitted write,
reply, and replacement text and to the --with fragment of edit.`,
		Args: argumentValidator(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			if err := requireIdentityPreferencesEnabled(deps); err != nil {
				return err
			}
			_, _, preferences, _, _, err := loadRequiredIdentityConfig(cmd, deps)
			if err != nil {
				return err
			}
			return writeFormattingStatus(cmd, rootOptions, preferences.Effective())
		},
	}
	command.AddCommand(
		newFormattingPolicyCommand(deps, rootOptions, true),
		newFormattingPolicyCommand(deps, rootOptions, false),
	)
	return command
}

func newFormattingPolicyCommand(deps Dependencies, rootOptions *rootOptions, enable bool) *cobra.Command {
	verb := "disable"
	short := "Disable one formatting module"
	if enable {
		verb = "enable"
		short = "Enable one formatting module"
	}
	return &cobra.Command{
		Use:   verb + " <em-dash-to-spaced-hyphen>",
		Short: short,
		Args:  argumentValidator(cobra.ExactArgs(1)),
		RunE: func(cmd *cobra.Command, args []string) error {
			module, known := textformat.ParseModule(args[0])
			if !known {
				return invalidArgument(cmd, "unknown formatting module "+args[0])
			}
			if err := requireIdentityPreferencesEnabled(deps); err != nil {
				return err
			}
			bound, document, preferences, store, path, err := loadRequiredIdentityConfig(cmd, deps)
			if err != nil {
				return err
			}
			setFormattingEnabled(&preferences, module, enable)
			if err := saveIdentityConfigDocument(bound, store, document, preferences); err != nil {
				return err
			}
			action := fmt.Sprintf("formatting module %s disabled", module)
			if enable {
				action = fmt.Sprintf("formatting module %s enabled", module)
			}
			return writeIdentityConfigReceipt(cmd, rootOptions, action, path, config.Merge(document, preferences))
		},
	}
}

func writeFormattingStatus(cmd *cobra.Command, rootOptions *rootOptions, settings config.Settings) error {
	if rootOptions.json {
		return writeJSON(cmd, map[string]interface{}{
			"ok":         true,
			"formatting": formattingStrings(settings.Formatting),
			"available":  []string{string(textformat.ModuleEmDashToSpacedHyphen)},
		})
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Enabled formatting: %s\n", textformat.List(settings.Formatting))
	fmt.Fprintf(cmd.OutOrStdout(), "Available formatting: %s\n", textformat.ModuleEmDashToSpacedHyphen)
	return nil
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
	bound := deps
	preferences := config.Preferences{}
	authResult := auth.Result{}
	authConfigured := false
	identityAvailable := false
	if document.Disabled {
		credentials, credentialErr := deps.credentialStore()
		if credentialErr != nil {
			return credentialErr
		}
		authResult, credentialErr = credentials.Get()
		authConfigured = credentialErr == nil
	} else {
		bound, document, preferences, authResult, identityAvailable, err = loadOptionalIdentityConfig(cmd, deps)
		if err != nil {
			return err
		}
		authConfigured = identityAvailable
	}
	settings := document.Effective()
	if identityAvailable {
		settings = config.Merge(document, preferences)
	}

	if rootOptions.json {
		payload := map[string]interface{}{
			"ok":              true,
			"path":            path,
			"disabled":        settings.Disabled,
			"deny_mutations":  mutationStrings(settings.DeniedMutations),
			"auth_configured": authConfigured,
			"auth_ignored":    authConfigured && settings.Disabled,
		}
		if authConfigured {
			payload["auth_source"] = authResult.Source
		}
		if identityAvailable {
			payload["identity"] = map[string]string{"team_id": bound.ActiveIdentity.TeamID, "user_id": bound.ActiveIdentity.UserID}
			payload["message_prefix"] = settings.MessagePrefix
			payload["message_prefix_source"] = prefixSource(preferences)
			payload["message_presentation"] = settings.MessagePresentation
			payload["message_presentation_source"] = presentationSource(preferences)
			payload["formatting"] = formattingStrings(settings.Formatting)
		}
		return writeJSON(cmd, payload)
	}

	writeConfigLocation(cmd, path)
	if settings.Disabled {
		fmt.Fprintln(cmd.OutOrStdout(), "Tool: disabled")
	} else {
		fmt.Fprintln(cmd.OutOrStdout(), "Tool: enabled")
	}
	fmt.Fprintf(cmd.OutOrStdout(), "Denied mutations: %s\n", mutationList(settings.DeniedMutations))
	if authConfigured {
		fmt.Fprintf(cmd.OutOrStdout(), "Authentication: configured (%s, %s)\n", authResult.Source, auth.MaskToken(authResult.Token))
		if settings.Disabled {
			fmt.Fprintln(cmd.OutOrStdout(), "Authentication use: ignored while slk is disabled")
		}
	} else {
		fmt.Fprintln(cmd.OutOrStdout(), "Authentication: not configured")
	}
	if identityAvailable {
		fmt.Fprintf(cmd.OutOrStdout(), "Canonical identity: %s / %s\n", bound.ActiveIdentity.TeamID, bound.ActiveIdentity.UserID)
		fmt.Fprintf(cmd.OutOrStdout(), "Message prefix (%s): %q\n", prefixSource(preferences), settings.MessagePrefix)
		fmt.Fprintf(cmd.OutOrStdout(), "Message presentation (%s): %s\n", presentationSource(preferences), settings.MessagePresentation)
		fmt.Fprintf(cmd.OutOrStdout(), "Enabled formatting: %s\n", textformat.List(settings.Formatting))
	} else {
		fmt.Fprintln(cmd.OutOrStdout(), "Identity preferences: unavailable until Slack authentication is validated")
	}
	if settings.Disabled {
		fmt.Fprintln(cmd.OutOrStdout(), "Agent guidance: ask the user for permission before running 'slk config enable'.")
	}
	return nil
}

func requireIdentityPreferencesEnabled(deps Dependencies) error {
	settings, err := deps.config()
	if err != nil {
		return err
	}
	if settings.Disabled {
		return identityPreferencesDisabledError()
	}
	return nil
}

func loadRequiredIdentityConfig(cmd *cobra.Command, deps Dependencies) (Dependencies, config.Document, config.Preferences, config.Store, string, error) {
	bound, document, preferences, _, configured, err := loadOptionalIdentityConfig(cmd, deps)
	if err != nil {
		return deps, config.Document{}, config.Preferences{}, nil, "", err
	}
	if !configured {
		return deps, config.Document{}, config.Preferences{}, nil, "", authRequiredError()
	}
	store, err := bound.configStore()
	if err != nil {
		return deps, config.Document{}, config.Preferences{}, nil, "", err
	}
	path, err := store.Path()
	if err != nil {
		return deps, config.Document{}, config.Preferences{}, nil, "", configLoadError(err)
	}
	return bound, document, preferences, store, path, nil
}

func loadOptionalIdentityConfig(cmd *cobra.Command, deps Dependencies) (Dependencies, config.Document, config.Preferences, auth.Result, bool, error) {
	credentials, err := deps.credentialStore()
	if err != nil {
		return deps, config.Document{}, config.Preferences{}, auth.Result{}, false, err
	}
	authResult, err := credentials.Get()
	if err != nil {
		return deps, config.Document{}, config.Preferences{}, auth.Result{}, false, nil
	}
	identityResult, err := deps.validateToken(cmd.Context(), authResult.Token, cmd.ErrOrStderr())
	if err != nil {
		return deps, config.Document{}, config.Preferences{}, auth.Result{}, false, identityUnavailableError(err)
	}
	bound, document, preferences, err := deps.bindIdentity(authResult.Token, identityResult)
	if err != nil {
		return deps, config.Document{}, config.Preferences{}, auth.Result{}, false, err
	}
	return bound, document, preferences, authResult, true, nil
}

func saveIdentityConfigDocument(deps Dependencies, store config.Store, document config.Document, preferences config.Preferences) error {
	if deps.ActiveIdentity == nil {
		return internalError()
	}
	if err := document.SetPreferences(*deps.ActiveIdentity, preferences); err != nil {
		return configSaveError(err)
	}
	if err := store.Save(document); err != nil {
		return configSaveError(err)
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
	document, err := store.Load()
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

func setFormattingEnabled(preferences *config.Preferences, module textformat.Module, enabled bool) {
	filtered := preferences.Formatting[:0]
	for _, existing := range preferences.Formatting {
		if existing != module {
			filtered = append(filtered, existing)
		}
	}
	preferences.Formatting = filtered
	if enabled {
		preferences.Formatting = append(preferences.Formatting, module)
	}
}

func writeConfigLocation(cmd *cobra.Command, path string) {
	fmt.Fprintf(cmd.OutOrStdout(), "Configuration: %s\n", path)
}

func writeConfigReceipt(cmd *cobra.Command, rootOptions *rootOptions, action, path string, settings config.Settings) error {
	if rootOptions.json {
		return writeJSON(cmd, map[string]interface{}{
			"ok":             true,
			"action":         action,
			"path":           path,
			"disabled":       settings.Disabled,
			"deny_mutations": mutationStrings(settings.DeniedMutations),
		})
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s.\n", strings.ToUpper(action[:1])+action[1:])
	writeConfigLocation(cmd, path)
	return nil
}

func writeIdentityConfigReceipt(cmd *cobra.Command, rootOptions *rootOptions, action, path string, settings config.Settings) error {
	if rootOptions.json {
		return writeJSON(cmd, map[string]interface{}{
			"ok":                   true,
			"action":               action,
			"path":                 path,
			"message_prefix":       settings.MessagePrefix,
			"message_presentation": settings.MessagePresentation,
			"formatting":           formattingStrings(settings.Formatting),
		})
	}
	fmt.Fprintf(cmd.OutOrStdout(), "%s.\n", strings.ToUpper(action[:1])+action[1:])
	writeConfigLocation(cmd, path)
	fmt.Fprintf(cmd.OutOrStdout(), "Message presentation: %s\n", settings.MessagePresentation)
	return nil
}

func prefixSource(preferences config.Preferences) string {
	if preferences.MessagePrefix == nil {
		return "default"
	}
	if *preferences.MessagePrefix == "" {
		return "disabled"
	}
	return "custom"
}

func presentationSource(preferences config.Preferences) string {
	if preferences.MessagePresentation == nil {
		return "default"
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

func formattingStrings(modules []textformat.Module) []string {
	values := textformat.Names(modules)
	sort.Strings(values)
	return values
}
