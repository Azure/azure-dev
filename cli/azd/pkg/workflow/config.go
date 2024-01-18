package workflow

// Stores a map of workflows configured for an azd project
type WorkflowMap map[string]*Workflow

// UnmarshalYAML will unmarshal the WorkflowMap from YAML.
// The unmarshalling will marshall the YAML like a standard Go map
// but will also persist the key as the workflow name within the Workflow struct.
func (wm *WorkflowMap) UnmarshalYAML(unmarshal func(interface{}) error) error {
	var m map[string]*Workflow
	if err := unmarshal(&m); err != nil {
		return err
	}

	for key, workflow := range m {
		workflow.Name = key
	}

	*wm = m

	return nil
}
