// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"fmt"
	"os"
	"strings"

	"azureaiagent/internal/exterrors"
)

// deploymentResolver checks for and creates a model deployment. The seam keeps
// the deployment node unit-testable without touching Azure.
type deploymentResolver interface {
	// Exists reports whether a deployment for modelName is present.
	Exists(ctx context.Context, modelName string) (bool, error)
	// Create creates the deployment for modelName. It must be idempotent.
	Create(ctx context.Context, modelName string) error
}

// deploymentNode builds the model-deployment graph node. It validates that a
// model is declared and, at resolve time, creates the deployment if missing.
// Returns nil when no model is declared (the agent node reports that error).
func deploymentNode(
	g *promptGraph,
	newResolver func() (deploymentResolver, error),
) *promptNode {
	model := strings.TrimSpace(g.managed.Model)
	if model == "" {
		return nil
	}
	return &promptNode{
		Kind: nodeDeployment,
		ID:   model,
		Validate: func() error {
			// The model name must be a simple deployment identifier.
			if strings.ContainsAny(model, " /\\") {
				return exterrors.Validation(
					exterrors.CodeInvalidAgentManifest,
					fmt.Sprintf("model %q is not a valid deployment name", model),
					"set 'model' to a model deployment name (e.g. gpt-4.1-mini)",
				)
			}
			return nil
		},
		Resolve: func(ctx context.Context) error {
			resolver, err := newResolver()
			if err != nil {
				return err
			}
			exists, err := resolver.Exists(ctx, model)
			if err != nil {
				return err
			}
			if exists {
				return nil
			}
			if err := resolver.Create(ctx, model); err != nil {
				return fmt.Errorf("creating model deployment %q: %w", model, err)
			}
			return nil
		},
	}
}

// provisionedDeploymentResolver is the live deploymentResolver. Model
// deployments for prompt agents are provisioned by azd infra (recorded at init
// and applied during `azd provision`), so at deploy time the deployment is
// assumed present. This resolver therefore treats every model as existing and
// never issues a data-plane create, but keeps the seam so the graph can enforce
// the create-if-missing contract in tests and future live wiring.
type provisionedDeploymentResolver struct{}

func (provisionedDeploymentResolver) Exists(context.Context, string) (bool, error) {
	return true, nil
}

func (provisionedDeploymentResolver) Create(_ context.Context, modelName string) error {
	// Should not be reached given Exists always returns true; guard defensively
	// with an actionable message rather than a silent no-op.
	fmt.Fprintf(os.Stderr,
		"Model deployment %q was not found. Provision it with `azd provision` "+
			"(deployments are declared in azure.yaml).\n", modelName)
	return nil
}
