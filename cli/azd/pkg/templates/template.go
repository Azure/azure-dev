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

	// Additional metadata about the template
	Metadata map[string]string `json:"metadata,omitempty"`
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
		{"RepositoryPath", ":", t.RepositoryPath},
		{"Name", ":", t.Name},
		{"Source", ":", t.Source},
		{"Description", ":", t.Description},
	}

	for _, line := range text {
		_, err := tabs.Write([]byte(strings.Join(line, "\t") + "\n"))
		if err != nil {
			return err
		}
	}

	return tabs.Flush()
}
