package kubectl

type Node struct {
	Name    string
	Status  string
	Roles   []string
	Version string
}

type ListResult struct {
	ApiVersion string           `json:"apiVersion"`
	Kind       string           `json:"kind"`
	Items      []map[string]any `json:"items"`
}
