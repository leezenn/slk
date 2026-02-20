package cmd

import (
	"bufio"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"strings"

	"github.com/leezenn/slk/internal/api"
	"github.com/leezenn/slk/internal/auth"
	"github.com/spf13/cobra"
)

var clearAuth bool

var authCmd = &cobra.Command{
	Use:   "auth [token]",
	Short: "Store or manage Slack API token",
	Long: `Store a Slack API token, show auth status, or clear stored credentials.

Requires a User OAuth Token (xoxp-). If the Slack app is already installed,
copy your token from OAuth & Permissions at https://api.slack.com/apps.`,
	Example: `  slk auth xoxp-your-token-here    # Store token (non-interactive)
  slk auth                          # Show status or guided setup
  slk auth --clear                  # Remove stored token`,
	Args: cobra.MaximumNArgs(1),
	Run: func(cmd *cobra.Command, args []string) {
		if clearAuth {
			if err := auth.ClearToken(); err != nil {
				fmt.Fprintf(os.Stderr, "Error clearing token: %v\n", err)
				os.Exit(1)
			}
			fmt.Printf("Token removed from %s.\n", credStoreName())
			return
		}

		if len(args) == 1 {
			storeToken(args[0])
			return
		}

		// No args: show status or guided setup
		result, err := auth.GetToken()
		if err != nil {
			guidedSetup()
			return
		}

		fmt.Printf("Status: configured\n")
		fmt.Printf("Source: %s\n", result.Source)
		fmt.Printf("Token:  %s\n", auth.MaskToken(result.Token))
	},
}

func storeToken(raw string) {
	token := strings.TrimSpace(raw)
	if token == "" {
		fmt.Fprintln(os.Stderr, "Error: empty token")
		os.Exit(1)
	}
	if !strings.HasPrefix(token, "xoxp-") {
		fmt.Fprintln(os.Stderr, "Error: expected a User OAuth Token (starts with xoxp-)")
		fmt.Fprintln(os.Stderr, "Bot tokens (xoxb-) are not supported — Slack's search and DM APIs require a user token.")
		os.Exit(1)
	}
	// Validate against Slack API before storing
	client := api.NewClient(token)
	result, err := client.AuthTest()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: token rejected by Slack: %v\n", err)
		os.Exit(1)
	}
	if err := auth.StoreToken(token); err != nil {
		fmt.Fprintf(os.Stderr, "Error storing token: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("Authenticated as @%s in %s.\n", result.User, result.Team)
	fmt.Printf("Token stored in %s.\n", credStoreName())
}

func guidedSetup() {
	// Handle Ctrl+C
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt)
	go func() {
		<-sig
		fmt.Fprintln(os.Stderr, "\nAborted.")
		os.Exit(130)
	}()

	fmt.Println("No token configured. Let's set one up.")
	fmt.Println()
	fmt.Println("You need a Slack User OAuth Token (xoxp-...).")
	fmt.Println()
	fmt.Println("If your workspace already has a Slack app installed:")
	fmt.Println("  1. Go to https://api.slack.com/apps")
	fmt.Println("  2. Select your app")
	fmt.Println("  3. OAuth & Permissions -> User OAuth Token -> Copy")
	fmt.Println()
	fmt.Println("If not, create one:")
	fmt.Println("  1. https://api.slack.com/apps -> Create New App -> From scratch")
	fmt.Println("  2. OAuth & Permissions -> add these User Token Scopes:")
	fmt.Println("     channels:history, channels:read, groups:history, groups:read,")
	fmt.Println("     im:history, im:read, mpim:history, mpim:read,")
	fmt.Println("     search:read, users:read, files:read")
	fmt.Println("  3. Install to Workspace -> Copy User OAuth Token")
	fmt.Println()
	fmt.Printf("Token will be stored in %s.\n", credStoreName())
	fmt.Println("For non-interactive use: slk auth <token>")
	fmt.Println("Or set SLACK_TOKEN env var.")
	fmt.Println()
	fmt.Print("Paste your xoxp- token: ")

	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		fmt.Fprintln(os.Stderr, "\nAborted.")
		os.Exit(130)
	}

	token := strings.TrimSpace(scanner.Text())
	if token == "" {
		fmt.Fprintln(os.Stderr, "No token provided.")
		os.Exit(1)
	}

	storeToken(token)
}

func credStoreName() string {
	switch runtime.GOOS {
	case "darwin":
		return "macOS Keychain"
	case "linux":
		return "Secret Service (GNOME Keyring)"
	case "windows":
		return "Windows Credential Manager"
	default:
		return "credential store"
	}
}

func init() {
	authCmd.Flags().BoolVar(&clearAuth, "clear", false, "Remove stored token")
	rootCmd.AddCommand(authCmd)
}
