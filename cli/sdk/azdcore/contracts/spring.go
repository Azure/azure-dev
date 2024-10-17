package contracts

// The Azure Spring Apps configuration options
type SpringOptions struct {
	// The deployment name of ASA app
	DeploymentName string `yaml:"deploymentName"`
}
