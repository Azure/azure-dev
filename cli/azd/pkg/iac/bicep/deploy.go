package bicep

import (
	"context"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
)

// Deploy a bicep template to a target using a set of parameters.
func Deploy(ctx context.Context, target DeploymentTarget, bicepPath string, parametersPath string) (azcli.AzCliDeployment, error) {
	// We've seen issues where `Deploy` completes but for a short while after, fetching the deployment fails with a `DeploymentNotFound` error.
	// Since other commands of ours use the deployment, let's try to fetch it here and if we fail with `DeploymentNotFound`,
	// ignore this error, wait a short while and retry.
	if err := target.Deploy(ctx, bicepPath, parametersPath); err != nil {
		return azcli.AzCliDeployment{}, fmt.Errorf("failed deploying: %w", err)
	}

	var deployment azcli.AzCliDeployment
	var err error

	for i := 0; i < 10; i++ {
		time.Sleep(time.Duration(math.Min(float64(i), 3)*10) * time.Second)
		deployment, err = target.GetDeployment(ctx)
		if errors.Is(err, azcli.ErrDeploymentNotFound) {
			continue
		} else if err != nil {
			return azcli.AzCliDeployment{}, fmt.Errorf("failed waiting for deployment: %w", err)
		} else {
			return deployment, nil
		}
	}

	return azcli.AzCliDeployment{}, fmt.Errorf("timed out waiting for deployment: %w", err)
}
