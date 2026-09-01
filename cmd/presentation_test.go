package cmd

import (
	"strings"
	"testing"

	"github.com/leezenn/slk/internal/presentation"
	"github.com/spf13/cobra"
)

func TestResolvePresentationUsesFlagOverConfig(t *testing.T) {
	tests := []struct {
		name       string
		configured presentation.Mode
		override   string
		setFlag    bool
		want       presentation.Mode
		wantErr    string
	}{
		{name: "built-in default", want: presentation.SlackManaged},
		{name: "configured mode", configured: presentation.AlwaysExpanded, want: presentation.AlwaysExpanded},
		{name: "flag overrides config", configured: presentation.AlwaysExpanded, override: "slack-managed", setFlag: true, want: presentation.SlackManaged},
		{name: "invalid flag", override: "forced", setFlag: true, wantErr: "must be slack-managed or always-expanded"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			command := &cobra.Command{Use: "test"}
			var raw string
			addPresentationFlag(command, &raw)
			if test.setFlag {
				if err := command.Flags().Set("presentation", test.override); err != nil {
					t.Fatal(err)
				}
				raw = test.override
			}

			got, err := resolvePresentation(command, raw, test.configured)
			if test.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantErr) {
					t.Fatalf("resolvePresentation() error = %v", err)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("resolvePresentation() = %q, %v; want %q", got, err, test.want)
			}
		})
	}
}

func TestPresentationHelpUsesConfiguredMode(t *testing.T) {
	help := presentationHelp(presentation.AlwaysExpanded)
	for _, want := range []string{"Effective default: always-expanded", "--presentation overrides this command only", "slack-managed", "always-expanded"} {
		if !strings.Contains(help, want) {
			t.Fatalf("presentation help omitted %q: %q", want, help)
		}
	}
}
