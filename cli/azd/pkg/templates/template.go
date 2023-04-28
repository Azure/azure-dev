package templates

type Template struct {
	Name           string `json:"name"`
	DisplayName    string `json:"displayName,omitempty"` // do not displayName in output contracts for now
	Description    string `json:"description"`
	RepositoryPath string `json:"repositoryPath"`
}
