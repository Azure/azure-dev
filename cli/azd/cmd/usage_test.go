package cmd

import (
	"bytes"
	"html/template"
	"strings"
	"testing"

	"github.com/azure/azure-dev/cli/azd/test/snapshot"
	"github.com/spf13/cobra"
	"github.com/stretchr/testify/require"
)

// To update snapshots (assuming your current directory is cli/azd):
//
// For Bash,
// UPDATE_SNAPSHOTS=true go test ./cmd
//
// For Pwsh,
// $env:UPDATE_SNAPSHOTS='true'; go test ./cmd; $env:UPDATE_SNAPSHOTS=$null
func TestUsage(t *testing.T) {
	// disable rich formatting output
	t.Setenv("TERM", "dumb")
	root := NewRootCmd(false, nil, nil)

	usageSnapshot(t, root)
}

func usageSnapshot(t *testing.T, cmd *cobra.Command) {
	t.Run(cmd.Name(), func(t *testing.T) {
		result, err := resolveTemplate(cmd.HelpTemplate(), cmd)
		require.NoError(t, err)
		snapshot.SnapshotT(t, result)

		for _, c := range cmd.Commands() {
			if !c.IsAvailableCommand() || c.IsAdditionalHelpTopicCommand() {
				continue
			}

			usageSnapshot(t, c)
		}
	})
}

func resolveTemplate(text string, data interface{}) (string, error) {
	finalBuffer := &bytes.Buffer{}
	t := template.New("resolve template with command")
	template.Must(t.Parse(text))

	if err := t.Execute(finalBuffer, data); err != nil {
		return "", err
	}
	// update `>` and `<`
	return strings.ReplaceAll(strings.ReplaceAll(finalBuffer.String(), "&lt;", "<"), "&gt;", ">"), nil
}
