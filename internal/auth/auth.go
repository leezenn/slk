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

// Store is the credential seam used by the command package.
type Store interface {
	Get() (Result, error)
	Set(token string) error
	Clear() error
}

type systemStore struct{}

// NewStore returns the platform credential store.
func NewStore() Store { return systemStore{} }

// Get retrieves the Slack token using the current compatibility precedence.
func (systemStore) Get() (Result, error) {
	token, err := credentialGet(serviceName, accountName)
	if err == nil && token != "" {
		return Result{Token: token, Source: SourceKeychain}, nil
	}

	if token := os.Getenv("SLACK_TOKEN"); token != "" {
		return Result{Token: token, Source: SourceEnv}, nil
	}

	return Result{Source: SourceNone}, fmt.Errorf("no token found. Run: slk auth <your-token>")
}

func (systemStore) Set(token string) error {
	return credentialSet(serviceName, accountName, token)
}

func (systemStore) Clear() error {
	return credentialDelete(serviceName, accountName)
}

// MaskToken returns a masked preview of a token (first 8 chars + ...).
func MaskToken(token string) string {
	if len(token) <= 8 {
		return "****"
	}
	return token[:8] + "..."
}
