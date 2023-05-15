package templates

import "fmt"

type Template struct {
	// Name is the friendly short name of the template.
	Name string `json:"name"`

	// Description is a long description of the template.
	Description string `json:"description"`

	// RepositoryPath is a fully qualified URI to a git repository,
	// "{owner}/{repo}" for GitHub repositories,
	// or "{repo}" for GitHub repositories under Azure-Samples (default organization).
	RepositoryPath string `json:"repositoryPath"`
}

// Display returns a string representation of the template suitable for display.
func (t *Template) Display() string {
	return fmt.Sprintf(
		"%s: %s\n%s: %s\n%s: %s",
		"RepositoryPath",
		t.RepositoryPath,
		"Name",
		t.Name,
		"Description",
		t.Description)
}
