package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/spf13/cobra"
)

// ErrorCode groups failures by the recovery available to the caller.
type ErrorCode string

const (
	ErrorInvalidArgument   ErrorCode = "invalid_argument"
	ErrorConflict          ErrorCode = "conflict"
	ErrorRefused           ErrorCode = "refused"
	ErrorAuthRequired      ErrorCode = "auth_required"
	ErrorAuthFailed        ErrorCode = "auth_failed"
	ErrorCredentialBackend ErrorCode = "credential_backend"
	ErrorSlackAPI          ErrorCode = "slack_api"
	ErrorFilesystem        ErrorCode = "filesystem"
	ErrorConfig            ErrorCode = "config"
	ErrorInternal          ErrorCode = "internal"
	ErrorInterrupted       ErrorCode = "interrupted"
)

// CommandError carries a semantic explanation and a useful recovery action.
type CommandError struct {
	Code      ErrorCode
	Message   string
	Action    string
	ShowUsage bool
}

func (e *CommandError) Error() string { return e.Message }

func newCommandError(code ErrorCode, message, action string) error {
	return &CommandError{Code: code, Message: message, Action: action}
}

func invalidArgument(cmd *cobra.Command, reason string) error {
	return &CommandError{
		Code:      ErrorInvalidArgument,
		Message:   "Invalid argument: " + safeDynamic(reason, 256),
		Action:    fmt.Sprintf("Run '%s --help' to see valid usage.", cmd.CommandPath()),
		ShowUsage: true,
	}
}

func conflictingOptions(cmd *cobra.Command, reason string) error {
	return &CommandError{
		Code:      ErrorConflict,
		Message:   "Conflicting options: " + safeDynamic(reason, 256),
		Action:    fmt.Sprintf("Run '%s --help' and choose one mode.", cmd.CommandPath()),
		ShowUsage: true,
	}
}

func authRequiredError() error {
	return newCommandError(
		ErrorAuthRequired,
		"Slack authentication is not configured.",
		"Run 'slk auth' to connect Slack, then retry.",
	)
}

func credentialBackendError(err error) error {
	return newCommandError(
		ErrorCredentialBackend,
		"The credential store could not complete the request: "+safeDynamic(err.Error(), 256)+".",
		"Check the platform credential service, then retry.",
	)
}

func slackAPIError(err error) error {
	return newCommandError(
		ErrorSlackAPI,
		"Slack could not complete the request: "+safeDynamic(err.Error(), 256)+".",
		"Check the conversation, Slack permissions, or connection, then retry.",
	)
}

func filesystemError(err error) error {
	return newCommandError(
		ErrorFilesystem,
		"The local file operation failed: "+safeDynamic(err.Error(), 256)+".",
		"Check the destination and its permissions, then retry.",
	)
}

func configLoadError(err error) error {
	return newCommandError(
		ErrorConfig,
		"slk could not load its configuration: "+safeDynamic(err.Error(), 256)+".",
		"Fix the configuration file, then retry.",
	)
}

func refusedError(message, action string) error {
	return newCommandError(ErrorRefused, message, action)
}

func internalError() error {
	return newCommandError(
		ErrorInternal,
		"slk could not finish the command because an internal dependency was unavailable.",
		"Retry the command; if it fails again, report the command and slk version.",
	)
}

func interruptedError() error {
	return newCommandError(ErrorInterrupted, "Operation interrupted.", "")
}

func argumentValidator(validate cobra.PositionalArgs) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if err := validate(cmd, args); err != nil {
			return invalidArgument(cmd, err.Error())
		}
		return nil
	}
}

func checkContext(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return interruptedError()
	}
	return nil
}

func normalizeCommandError(err error, root *cobra.Command, args []string) *CommandError {
	var commandErr *CommandError
	if errors.As(err, &commandErr) {
		return commandErr
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return interruptedError().(*CommandError)
	}
	return invalidArgument(commandForArgs(root, args), err.Error()).(*CommandError)
}

func commandForArgs(root *cobra.Command, args []string) *cobra.Command {
	command, _, err := root.Find(args)
	if err == nil && command != nil {
		return command
	}
	for _, candidate := range root.Commands() {
		for _, arg := range args {
			if arg == candidate.Name() {
				return candidate
			}
		}
	}
	return root
}

func renderError(err error, root *cobra.Command, args []string, errOut io.Writer) int {
	commandErr := normalizeCommandError(err, root, args)
	var rendered strings.Builder
	fmt.Fprintln(&rendered, commandErr.Message)
	if commandErr.Action != "" {
		fmt.Fprintln(&rendered, commandErr.Action)
	}
	if commandErr.ShowUsage {
		fmt.Fprintln(&rendered)
		rendered.WriteString(commandForArgs(root, args).UsageString())
	}
	_, _ = io.WriteString(errOut, rendered.String())
	if commandErr.Code == ErrorInterrupted {
		return 130
	}
	return 1
}

var secretPattern = regexp.MustCompile(`(?i)(?:xox[a-z]-)[A-Za-z0-9-]+`)
var privateURLPattern = regexp.MustCompile(`(?i)https://[^\s]*slack\.com/[^\s]*`)

func safeDynamic(value string, maxBytes int) string {
	value = strings.Join(strings.Fields(value), " ")
	value = secretPattern.ReplaceAllString(value, "[redacted]")
	value = privateURLPattern.ReplaceAllString(value, "[redacted]")
	if len(value) <= maxBytes {
		return value
	}
	value = value[:maxBytes]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value
}
