package templates

type Template struct {
	Name           string `json:"name"`
	DisplayName    string `json:"displayName,omitempty"`
	Description    string `json:"description"`
	RepositoryPath string `json:"repositoryPath"`
}
