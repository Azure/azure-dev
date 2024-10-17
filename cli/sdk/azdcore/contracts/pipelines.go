package contracts

// options supported in azure.yaml
type PipelineOptions struct {
	Provider  string   `yaml:"provider"`
	Variables []string `yaml:"variables"`
	Secrets   []string `yaml:"secrets"`
}
