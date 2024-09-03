package templates

import (
	"encoding/json"
	"fmt"
)

// newJsonTemplateSource creates a new template source from a JSON string.
func newJsonTemplateSource(name string, jsonTemplates string) (Source, error) {
	var templates []*Template
	err := json.Unmarshal([]byte(jsonTemplates), &templates)
	if err != nil {
		return nil, fmt.Errorf("unable to unmarshal templates JSON %w", err)
	}

	return newTemplateSource(name, templates)
}
