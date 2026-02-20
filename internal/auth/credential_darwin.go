package auth

import (
	"fmt"
	"os/exec"
	"strings"
)

func credentialGet(service, account string) (string, error) {
	out, err := exec.Command("security", "find-generic-password",
		"-s", service, "-a", account, "-w").Output()
	if err != nil {
		return "", fmt.Errorf("keychain: %w", err)
	}
	return strings.TrimSpace(string(out)), nil
}

func credentialSet(service, account, secret string) error {
	return exec.Command("security", "add-generic-password",
		"-U", "-s", service, "-a", account, "-w", secret).Run()
}

func credentialDelete(service, account string) error {
	return exec.Command("security", "delete-generic-password",
		"-s", service, "-a", account).Run()
}
