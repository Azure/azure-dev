package templates

type Template struct {
	// Name is the friendly short name of the template.
	Name string `json:"name"`

	// Description is a long description of the template.
	Description string `json:"description"`

	// Path is a fully qualified URI to a git repository,
	// "{owner}/{repo}" for GitHub repositories,
	// or "{repo}" for GitHub repositories under Azure-Samples (default organization).
	Path string `json:"path"`
}

// ContractedTemplate is the template contract that is used expose by the CLI.
//
// Use NewContract to create a ContractedTemplate from a Template.
type ContractedTemplate struct {
	Template

	// RepositoryPath is always set to the same value as template.Path
	// It is added for backwards compatibility with old versions of azd where repositoryPath was surfaced in the contract.
	// This can be removed once backwards compatibility is no longer required.
	RepositoryPath string `json:"repositoryPath"`
}

func NewContract(template Template) ContractedTemplate {
	return ContractedTemplate{
		Template:       template,
		RepositoryPath: template.Path,
	}
}
