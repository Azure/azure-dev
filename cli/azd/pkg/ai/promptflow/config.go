package promptflow

type Flow struct {
	Name        string            `json:"name"`
	Description string            `json:"description"`
	Type        FlowType          `json:"type"`
	Path        string            `json:"path"`
	Code        string            `json:"code"`
	DisplayName string            `json:"display_name"`
	Tags        map[string]string `json:"tags"`
}

type FlowType string

const (
	FlowTypeChat FlowType = "chat"
)
