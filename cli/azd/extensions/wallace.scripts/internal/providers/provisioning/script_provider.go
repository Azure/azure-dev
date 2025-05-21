package provisioning

import (
	"context"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

type ScriptProvider struct {
}

func NewScriptProvider() azdext.ProvisioningProvider {
	return &ScriptProvider{}
}

var (
	_ azdext.ProvisioningProvider = &ScriptProvider{}
)

func (s *ScriptProvider) Name(ctx context.Context) (string, error) {
	fmt.Println("ScriptProvider.Name called")
	return "script", nil
}

func (s *ScriptProvider) Initialize(ctx context.Context, projectPath string, options *azdext.ProvisioningOptions) error {
	fmt.Printf("ScriptProvider.Initialize called: projectPath=%s\n", projectPath)
	return nil
}

func (s *ScriptProvider) State(ctx context.Context, options *azdext.ProvisioningStateOptions) (*azdext.ProvisioningStateResult, error) {
	fmt.Println("ScriptProvider.State called")
	return &azdext.ProvisioningStateResult{}, nil
}

func (s *ScriptProvider) Deploy(ctx context.Context) (*azdext.ProvisioningDeployResult, error) {
	fmt.Println("ScriptProvider.Deploy called")
	return &azdext.ProvisioningDeployResult{}, nil
}

func (s *ScriptProvider) Preview(ctx context.Context) (*azdext.ProvisioningDeployPreviewResult, error) {
	fmt.Println("ScriptProvider.Preview called")
	return &azdext.ProvisioningDeployPreviewResult{}, nil
}

func (s *ScriptProvider) Destroy(ctx context.Context, options *azdext.ProvisioningDestroyOptions) (*azdext.ProvisioningDestroyResult, error) {
	fmt.Println("ScriptProvider.Destroy called")
	return &azdext.ProvisioningDestroyResult{}, nil
}

func (s *ScriptProvider) Parameters(ctx context.Context) ([]*azdext.ProvisioningParameter, error) {
	fmt.Println("ScriptProvider.Parameters called")
	return []*azdext.ProvisioningParameter{}, nil
}
