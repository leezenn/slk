package config

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
)

const identityNamespaceVersion = "slk-identity-v1"

// Identity is the complete canonical Slack identity returned by auth.test.
type Identity struct {
	TeamID string
	UserID string
}

// NewIdentity rejects incomplete auth.test identity values.
func NewIdentity(teamID, userID string) (Identity, error) {
	if strings.TrimSpace(teamID) == "" || strings.TrimSpace(userID) == "" ||
		strings.TrimSpace(teamID) != teamID || strings.TrimSpace(userID) != userID {
		return Identity{}, errors.New("auth.test must return complete canonical team_id and user_id values")
	}
	return Identity{TeamID: teamID, UserID: userID}, nil
}

// Namespace derives an opaque stable local key without exposing Slack IDs.
func (i Identity) Namespace() (string, error) {
	identity, err := NewIdentity(i.TeamID, i.UserID)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256([]byte(identityNamespaceVersion + "\x00" + identity.TeamID + "\x00" + identity.UserID))
	return hex.EncodeToString(digest[:]), nil
}

func validNamespace(value string) bool {
	if len(value) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(value)
	return err == nil
}
