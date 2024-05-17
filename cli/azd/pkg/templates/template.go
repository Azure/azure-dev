package templates

import (
	"io"
	"strings"
	"text/tabwriter"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

type Template struct {
	Id string `json:"id"`

	// Name is the friendly short name of the template.
	Name string `json:"name"`

	// The source of the template
	Source string `json:"source,omitempty"`

	// Description is a long description of the template.
	Description string `json:"description,omitempty"`

	// RepositoryPath is a fully qualified URI to a git repository,
	// "{owner}/{repo}" for GitHub repositories,
	// or "{repo}" for GitHub repositories under Azure-Samples (default organization).
	RepositoryPath string `json:"repositoryPath"`

	// A list of tags associated with the template
	Tags []string `json:"tags"`

	// Additional metadata about the template
	Metadata Metadata `json:"metadata,omitempty"`
}

// Metadata contains additional metadata about the template
// This metadata is used to modify azd project, environment config and environment variables during azd init commands.
type Metadata struct {
	Variables map[string]string `json:"variables,omitempty"`
	Config    map[string]string `json:"config,omitempty"`
	Project   map[string]string `json:"project,omitempty"`
}

// Display writes a string representation of the template suitable for display.
func (t *Template) Display(writer io.Writer) error {
	tabs := tabwriter.NewWriter(
		writer,
		0,
		output.TableTabSize,
		1,
		output.TablePadCharacter,
		output.TableFlags)
	text := [][]string{
		{"RepositoryPath", ":", Hyperlink(t.RepositoryPath)},
		{"Name", ":", t.Name},
		{"Source", ":", t.Source},
		{"Description", ":", t.Description},
		{"Tags", ":", strings.Join(t.Tags, ", ")},
	}

	for _, line := range text {
		_, err := tabs.Write([]byte(strings.Join(line, "\t") + "\n"))
		if err != nil {
			return err
		}
	}

	return tabs.Flush()
}
