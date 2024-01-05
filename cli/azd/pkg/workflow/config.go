package workflow

// Stores a map of workflows configured for an azd project
type WorkflowMap struct {
	inner map[string]*Workflow
}

// NewWorkflowMap creates a new WorkflowMap.
func NewWorkflowMap() *WorkflowMap {
	return &WorkflowMap{
		inner: map[string]*Workflow{},
	}
}

// Set adds or updates a key-value pair in the map.
func (wm *WorkflowMap) Set(key string, value *Workflow) {
	if wm.inner == nil {
		wm.inner = map[string]*Workflow{}
	}

	wm.inner[key] = value
}

// Get retrieves the value for a given key from the map.
func (wm *WorkflowMap) Get(key string) (*Workflow, bool) {
	if wm.inner == nil {
		wm.inner = map[string]*Workflow{}
	}

	val, ok := wm.inner[key]
	return val, ok
}

// MarshalYAML marshals the WorkflowMap into YAML.
func (wm *WorkflowMap) MarshalYAML() (interface{}, error) {
	return wm.inner, nil
}

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

	wm.inner = m
	return nil
}
