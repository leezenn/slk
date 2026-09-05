package cmd

import (
	"errors"
	"fmt"

	"github.com/leezenn/slk/internal/profile"
	"github.com/spf13/cobra"
)

func newStyleCommand(deps Dependencies, rootOptions *rootOptions) *cobra.Command {
	command := &cobra.Command{
		Use:   "style",
		Short: "Show the authenticated user's general style profile",
		Long: `Inspect the authenticated user's local general style profile.

Only an approved revision may be used. Drafts require exact human review and
approval. Apply relevant linguistic patterns to the current message intent and
context; do not mechanically reproduce every feature. Inspect the relevant
message or thread separately before drafting.`,
		Args: argumentValidator(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			bound, err := bindStyleIdentity(cmd, deps)
			if err != nil {
				return err
			}
			store, err := bound.profileStore()
			if err != nil {
				return err
			}
			status, err := store.Status(*bound.ActiveIdentity)
			if err != nil {
				return styleStoreError(err)
			}
			return writeStyleStatus(cmd, rootOptions, status)
		},
	}
	command.AddCommand(
		newStylePrepareCommand(deps, rootOptions),
		newStyleCreateCommand(deps, rootOptions),
		newStyleUseCommand(deps, rootOptions),
		newStyleReviewCommand(deps, rootOptions),
		newStyleAdjustCommand(deps, rootOptions),
		newStyleApproveCommand(deps, rootOptions),
	)
	return command
}

func newStyleUseCommand(deps Dependencies, rootOptions *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "use",
		Short: "Read the approved general style profile",
		Long: `Read the approved general profile for stable voice guidance.

This command never selects a draft, changes profile state, reads conversation
context, or sends a Slack message. Apply relevant linguistic patterns to the
current message intent and context; do not mechanically reproduce every feature.`,
		Args: argumentValidator(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			bound, err := bindStyleIdentity(cmd, deps)
			if err != nil {
				return err
			}
			store, err := bound.profileStore()
			if err != nil {
				return err
			}
			revision, err := store.Use(*bound.ActiveIdentity)
			if err != nil {
				return styleUseError(err)
			}
			return writeStyleRevision(cmd, rootOptions, profile.StateApproved, revision)
		},
	}
}

func newStyleReviewCommand(deps Dependencies, rootOptions *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "review",
		Short: "Read the exact current draft for human review",
		Args:  argumentValidator(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			bound, err := bindStyleIdentity(cmd, deps)
			if err != nil {
				return err
			}
			store, err := bound.profileStore()
			if err != nil {
				return err
			}
			revision, err := store.Review(*bound.ActiveIdentity)
			if err != nil {
				return styleReviewError(err)
			}
			return writeStyleRevision(cmd, rootOptions, profile.StateDraft, revision)
		},
	}
}

func newStyleAdjustCommand(deps Dependencies, rootOptions *rootOptions) *cobra.Command {
	return &cobra.Command{
		Use:   "adjust",
		Short: "Replace the current draft with semantic JSON from stdin",
		Long: `Create a replacement draft from one strict semantic JSON object on stdin.

The object may contain only allowlisted profile content. Evidence, timestamps,
paths, prompts, source messages, and lifecycle state are not accepted.
Adjustment never activates a revision immediately.

` + styleSemanticFieldsHelp,
		Args: argumentValidator(cobra.NoArgs),
		RunE: func(cmd *cobra.Command, _ []string) error {
			content, err := profile.DecodeContent(cmd.InOrStdin())
			if err != nil {
				return invalidArgument(cmd, err.Error())
			}
			bound, err := bindStyleIdentity(cmd, deps)
			if err != nil {
				return err
			}
			now, err := bound.now()
			if err != nil {
				return err
			}
			store, err := bound.profileStore()
			if err != nil {
				return err
			}
			revision, err := store.Adjust(*bound.ActiveIdentity, content, now)
			if err != nil {
				return styleAdjustError(err)
			}
			return writeStyleRevision(cmd, rootOptions, profile.StateDraft, revision)
		},
	}
}

func newStyleApproveCommand(deps Dependencies, rootOptions *rootOptions) *cobra.Command {
	var digest string
	command := &cobra.Command{
		Use:   "approve --digest <sha256>",
		Short: "Approve only the exact current reviewed draft",
		Long: `Promote only the current draft identified by its review digest.

Missing, replaced, or stale drafts fail without changing the approved revision.`,
		Args: argumentValidator(cobra.NoArgs),
		PreRunE: func(cmd *cobra.Command, _ []string) error {
			if digest == "" {
				return invalidArgument(cmd, "--digest is required from 'slk style review'")
			}
			return nil
		},
		RunE: func(cmd *cobra.Command, _ []string) error {
			bound, err := bindStyleIdentity(cmd, deps)
			if err != nil {
				return err
			}
			now, err := bound.now()
			if err != nil {
				return err
			}
			store, err := bound.profileStore()
			if err != nil {
				return err
			}
			revision, err := store.Approve(*bound.ActiveIdentity, digest, now)
			if err != nil {
				return styleApproveError(err)
			}
			return writeStyleRevision(cmd, rootOptions, profile.StateApproved, revision)
		},
	}
	command.Flags().StringVar(&digest, "digest", "", "Exact reviewed draft digest")
	return command
}

func bindStyleIdentity(cmd *cobra.Command, deps Dependencies) (Dependencies, error) {
	bound, _, err := bindCommandIdentity(cmd, deps)
	if err != nil {
		return deps, err
	}
	if bound.ActiveIdentity == nil {
		return deps, internalError()
	}
	return bound, nil
}

const styleContextGuidance = "Apply relevant linguistic patterns to the current message intent and context; do not mechanically reproduce every feature. Inspect the relevant message or thread before drafting."

const styleIncompatibleGuidance = "The existing file is unchanged. Automatic replacement is unavailable; ask the human how to handle it before collecting new evidence."

type styleContinuation struct {
	Guidance string `json:"guidance"`
	Command  string `json:"command,omitempty"`
}

type styleProfileStatus struct {
	profile.Status
	Continuation styleContinuation `json:"continuation"`
}

func writeStyleStatus(cmd *cobra.Command, rootOptions *rootOptions, status profile.Status) error {
	item := styleProfileStatus{Status: status, Continuation: statusContinuation(status)}
	if rootOptions.json {
		return writeJSON(cmd, map[string]interface{}{
			"ok":               true,
			"scope":            "general",
			"review_policy":    "required",
			"profile":          item,
			"context_guidance": styleContextGuidance,
		})
	}
	fmt.Fprintln(cmd.OutOrStdout(), "General style profile for the authenticated identity")
	fmt.Fprintln(cmd.OutOrStdout(), "Review policy: required")
	fmt.Fprintf(cmd.OutOrStdout(), "- general: %s", status.State)
	if status.Count > 0 {
		fmt.Fprintf(cmd.OutOrStdout(), " (%d qualifying messages)", status.Count)
	}
	fmt.Fprintln(cmd.OutOrStdout())
	fmt.Fprintf(cmd.OutOrStdout(), "  %s\n", item.Continuation.Guidance)
	fmt.Fprintln(cmd.OutOrStdout(), styleContextGuidance)
	return nil
}

func statusContinuation(status profile.Status) styleContinuation {
	switch status.State {
	case profile.StateAbsent:
		return styleContinuation{
			Guidance: "Ask the human whether they want a general profile created; after approval, collect evidence with 'slk --json style prepare'.",
			Command:  "slk --json style prepare",
		}
	case profile.StateDraft:
		return styleContinuation{Guidance: "Review the current draft with 'slk style review'; adjust or approve only that exact draft.", Command: "slk style review"}
	case profile.StateApprovedWithDraft:
		return styleContinuation{Guidance: "The approved revision remains usable; review the replacement with 'slk style review'.", Command: "slk style review"}
	case profile.StateApproved:
		return styleContinuation{Guidance: "Use the approved profile with 'slk style use', then inspect current context separately.", Command: "slk style use"}
	case profile.StateIncompatible:
		return styleContinuation{Guidance: styleIncompatibleGuidance}
	default:
		return styleContinuation{Guidance: "The profile state is unusable; ask the human how to handle the existing file before continuing."}
	}
}

func writeStyleRevision(cmd *cobra.Command, rootOptions *rootOptions, state profile.State, revision profile.Revision) error {
	result := map[string]interface{}{
		"ok":               true,
		"state":            state,
		"scope":            "general",
		"revision":         revision,
		"context_guidance": styleContextGuidance,
	}
	if state == profile.StateDraft {
		result["approve_command"] = fmt.Sprintf("slk style approve --digest %s", revision.Digest)
	}
	if rootOptions.json {
		return writeJSON(cmd, result)
	}
	fmt.Fprintln(cmd.OutOrStdout(), "Style profile: general")
	fmt.Fprintf(cmd.OutOrStdout(), "State: %s\n", state)
	fmt.Fprintf(cmd.OutOrStdout(), "Digest: %s\n", revision.Digest)
	fmt.Fprintf(cmd.OutOrStdout(), "Evidence: %d qualifying messages (%s to %s; %s)\n",
		revision.Coverage.Count,
		revision.Coverage.WindowFrom.Format("2006-01-02T15:04:05Z07:00"),
		revision.Coverage.WindowTo.Format("2006-01-02T15:04:05Z07:00"),
		revision.Coverage.Completion,
	)
	writeStringList(cmd, "Language patterns", revision.Content.LanguagePatterns)
	writeStringList(cmd, "Limitations", revision.Content.Limitations)
	if len(revision.Content.SyntheticExamples) > 0 {
		writeStringList(cmd, "Clearly synthetic examples", revision.Content.SyntheticExamples)
	}
	if state == profile.StateDraft {
		fmt.Fprintf(cmd.OutOrStdout(), "Approve exactly: slk style approve --digest %s\n", revision.Digest)
	}
	fmt.Fprintln(cmd.OutOrStdout(), styleContextGuidance)
	return nil
}

func writeStringList(cmd *cobra.Command, label string, values []string) {
	fmt.Fprintf(cmd.OutOrStdout(), "%s:\n", label)
	for _, value := range values {
		fmt.Fprintf(cmd.OutOrStdout(), "  - %s\n", value)
	}
}

func styleUseError(err error) error {
	switch {
	case errors.Is(err, profile.ErrNotFound):
		return refusedError("No approved general style profile exists for the authenticated identity.", "Ask the human whether they want a general profile created; do not invent one.")
	case errors.Is(err, profile.ErrDraftOnly):
		return refusedError("The general style profile is draft-only and cannot be used.", "Run 'slk style review', then adjust or approve the exact draft.")
	default:
		return styleStoreError(err)
	}
}

func styleReviewError(err error) error {
	if errors.Is(err, profile.ErrNotFound) || errors.Is(err, profile.ErrNoDraft) {
		return refusedError("No current general draft exists for review.", "List the profile with 'slk style' and review only a reported draft.")
	}
	return styleUseError(err)
}

func styleAdjustError(err error) error {
	if errors.Is(err, profile.ErrNotFound) {
		return refusedError("No general profile exists to adjust.", "Ask the human whether to create one through the evidence workflow; adjustment cannot invent an initial profile.")
	}
	return styleUseError(err)
}

func styleApproveError(err error) error {
	if errors.Is(err, profile.ErrNotFound) || errors.Is(err, profile.ErrStaleApproval) || errors.Is(err, profile.ErrNoDraft) {
		return newCommandError(ErrorConflict, "The reviewed draft is missing, replaced, or stale; nothing was approved.", "Run 'slk style review' again and approve its exact current digest.")
	}
	return styleStoreError(err)
}

func styleStoreError(err error) error {
	switch {
	case errors.Is(err, profile.ErrIncompatible):
		return refusedError("The style profile schema is incompatible and unusable.", styleIncompatibleGuidance)
	case errors.Is(err, profile.ErrInvalidDocument):
		return newCommandError(ErrorConfig, "The local style profile document is invalid.", "Ask the human how to handle the existing file before continuing.")
	default:
		return newCommandError(ErrorFilesystem, "The local style profile operation failed.", "Check local configuration storage and permissions, then retry.")
	}
}
