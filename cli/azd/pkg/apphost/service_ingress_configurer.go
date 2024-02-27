package apphost

import (
	"context"
	"slices"

	"github.com/azure/azure-dev/cli/azd/pkg/input"
)

type IngressSelector struct {
	manifest *Manifest
	console  input.Console
}

func NewIngressSelector(manifest *Manifest, console input.Console) *IngressSelector {
	return &IngressSelector{
		manifest: manifest,
		console:  console,
	}
}

func (adc *IngressSelector) SelectPublicServices(ctx context.Context) ([]string, error) {
	var services []string
	for name, res := range adc.manifest.Resources {
		// "container.v0" not supported on aspire-prev4
		// see: https://github.com/Azure/azure-dev/issues/3441
		if (res.Type == "project.v0" || res.Type == "dockerfile.v0") && len(res.Bindings) > 0 {
			services = append(services, name)
		}
	}

	if len(services) == 0 {
		return nil, nil
	}

	slices.Sort(services)

	adc.console.Message(ctx, "By default, a service can only be reached from inside the Azure Container Apps environment "+
		"it is running in. Selecting a service here will also allow it to be reached from the Internet.")

	exposed, err := adc.console.MultiSelect(ctx, input.ConsoleOptions{
		Message:      "Select which services to expose to the Internet",
		Options:      services,
		DefaultValue: []string{},
	})
	if err != nil {
		return nil, err
	}

	return exposed, nil
}
