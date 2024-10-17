package contracts

import "fmt"

type ProvisioningProviderKind string

const (
	NotSpecified ProvisioningProviderKind = ""
	Bicep        ProvisioningProviderKind = "bicep"
	Arm          ProvisioningProviderKind = "arm"
	Terraform    ProvisioningProviderKind = "terraform"
	Pulumi       ProvisioningProviderKind = "pulumi"
	Test         ProvisioningProviderKind = "test"
)

type ProvisioningOptions struct {
	Provider         ProvisioningProviderKind `yaml:"provider,omitempty"`
	Path             string                   `yaml:"path,omitempty"`
	Module           string                   `yaml:"module,omitempty"`
	DeploymentStacks map[string]any           `yaml:"deploymentStacks,omitempty"`
	// Not expected to be defined at azure.yaml
	IgnoreDeploymentState bool `yaml:"-"`
}

// Parses the specified IaC Provider to ensure whether it is valid or not
// Defaults to `Bicep` if no provider is specified
func ParseProvisioningProvider(kind ProvisioningProviderKind) (ProvisioningProviderKind, error) {
	switch kind {
	// For the time being we need to include `Test` here for the unit tests to work as expected
	// App builds will pass this test but fail resolving the provider since `Test` won't be registered in the container
	case NotSpecified, Bicep, Terraform, Test:
		return kind, nil
	}

	return ProvisioningProviderKind(""), fmt.Errorf("unsupported IaC provider '%s'", kind)
}
