package auth

import (
	"fmt"
	"os"
)

const (
	serviceName = "slk"
	accountName = "slack-token"
)

// Source describes where the token was found.
type Source string

const (
	SourceKeychain Source = "keychain"
	SourceEnv      Source = "env"
	SourceNone     Source = "none"
)

// Result holds a token and its source.
type Result struct {
	Token  string
	Source Source
}

// GetToken retrieves the Slack token in priority order:
// 1. OS credential store (Keychain / Secret Service / Credential Manager)
// 2. SLACK_TOKEN env var
// 3. Error
func GetToken() (Result, error) {
	token, err := credentialGet(serviceName, accountName)
	if err == nil && token != "" {
		return Result{Token: token, Source: SourceKeychain}, nil
	}

	if token := os.Getenv("SLACK_TOKEN"); token != "" {
		return Result{Token: token, Source: SourceEnv}, nil
	}

	return Result{Source: SourceNone}, fmt.Errorf("no token found. Run: slk auth <your-token>")
}

// StoreToken saves a token to the OS credential store.
func StoreToken(token string) error {
	return credentialSet(serviceName, accountName, token)
}

// ClearToken removes the token from the OS credential store.
func ClearToken() error {
	return credentialDelete(serviceName, accountName)
}

// MaskToken returns a masked preview of a token (first 8 chars + ...).
func MaskToken(token string) string {
	if len(token) <= 8 {
		return "****"
	}
	return token[:8] + "..."
}
