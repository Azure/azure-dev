package extensions

type Extension struct {
	Name        string `json:"name"`
	DisplayName string `json:"displayName"`
	Description string `json:"description"`
	Version     string `json:"version"`
	Usage       string `json:"usage"`
	Path        string `json:"path"`
}
