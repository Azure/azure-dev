package workflow

type Step struct {
	Command string   `yaml:"command,omitempty"`
	Args    []string `yaml:"args,omitempty"`
}

type Workflow struct {
	Name  string  `yaml:"name,omitempty"`
	Steps []*Step `yaml:"steps,omitempty"`
}
