package container

import "github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning/bicep"

// Register azd dependencies
func RegisterDependencies() {
	registerInfraProviders()
}

// Register infra provisioning providers.
func registerInfraProviders() {
	bicep.Register()
}
