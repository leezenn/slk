package auth

import (
	"fmt"
	"os/exec"
	"strings"
)

func credentialGet(service, account string) (string, error) {
	out, err := exec.Command("secret-tool", "lookup",
		"service", service, "account", account).Output()
	if err != nil {
		return "", fmt.Errorf("secret-tool: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func credentialSet(service, account, secret string) error {
	cmd := exec.Command("secret-tool", "store",
		"--label", service, "service", service, "account", account)
	cmd.Stdin = strings.NewReader(secret)
	return cmd.Run()
}

func credentialDelete(service, account string) error {
	return exec.Command("secret-tool", "clear",
		"service", service, "account", account).Run()
}
