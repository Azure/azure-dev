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

type ContractedTemplate struct {
	Template

	// RepositoryPath is always set to the same value as template.Path
	// It is added for backwards compatibility with the old contract.
	// This can be removed once the old contract is no longer used.
	RepositoryPath string `json:"repositoryPath"`
}

func NewContract(template Template) ContractedTemplate {
	return ContractedTemplate{
		Template:       template,
		RepositoryPath: template.Path,
	}
}
