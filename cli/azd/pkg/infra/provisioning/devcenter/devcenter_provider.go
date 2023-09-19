package devcenter

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/pkg/devcentersdk"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	. "github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
)

type DevCenterProvider struct {
	env             *environment.Environment
	devCenterClient devcentersdk.DevCenterClient
}

func NewDevCenterProvider(
	env *environment.Environment,
	devCenterClient devcentersdk.DevCenterClient,
) Provider {
	return &DevCenterProvider{
		env:             env,
		devCenterClient: devCenterClient,
	}
}

func (p *DevCenterProvider) Name() string {
	return "Dev Center"
}

func (p *DevCenterProvider) Initialize(ctx context.Context, projectPath string, options Options) error {
	return nil
}

func (p *DevCenterProvider) State(ctx context.Context, options *StateOptions) (*StateResult, error) {
	result := &StateResult{
		State: &State{},
	}

	return result, nil
}

func (p *DevCenterProvider) Deploy(ctx context.Context) (*DeployResult, error) {
	result := &DeployResult{
		Deployment: &Deployment{},
	}

	return result, nil
}

func (p *DevCenterProvider) Preview(ctx context.Context) (*DeployPreviewResult, error) {
	result := &DeployPreviewResult{
		Preview: &DeploymentPreview{},
	}

	return result, nil
}

func (p *DevCenterProvider) Destroy(ctx context.Context, options DestroyOptions) (*DestroyResult, error) {
	result := &DestroyResult{}

	return result, nil
}

func (p *DevCenterProvider) EnsureEnv(ctx context.Context) error {
	return nil
}
