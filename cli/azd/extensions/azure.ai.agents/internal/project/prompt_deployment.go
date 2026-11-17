// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"azureaiagent/internal/exterrors"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
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
					fmt.Sprintf("model deployment name %q is not valid", model),
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
				if _, ok := errors.AsType[*azdext.LocalError](err); ok {
					return err
				}
				return fmt.Errorf("creating model deployment %q: %w", model, err)
			}
			return nil
		},
	}
}

// provisionedDeploymentResolver is the live deploymentResolver. Model
// deployments for prompt agents are provisioned by azd infra (recorded at init
// and applied during `azd provision`), so at deploy time the deployment must
// already exist. This resolver checks for pending model deployment state
// and never issues a data-plane create, while keeping the seam so the graph can
// enforce the create-if-missing contract in tests and future live wiring.
type provisionedDeploymentResolver struct {
	graph *promptGraph
}

func (r provisionedDeploymentResolver) Exists(_ context.Context, modelName string) (bool, error) {
	if r.graph != nil && r.graph.env != nil {
		if hasPendingModelOrProjectProvision(r.graph.env["AI_AGENT_PENDING_PROVISION"]) {
			return false, nil
		}
		if r.graph.agentService != nil && r.graph.projectServices != nil {
			lookup := func(name string) string {
				if value, ok := r.graph.env[name]; ok {
					return value
				}
				value, _ := os.LookupEnv(name)
				return value
			}
			declared, err := usedProjectDeploymentNames(
				r.graph.agentService, r.graph.projectServices, r.graph.projectRoot, lookup,
			)
			if err != nil {
				return false, err
			}
			if containsDeploymentName(declared, modelName) {
				onDisk, err := promptModelInfrastructureOnDisk(r.graph.projectRoot)
				if err != nil {
					return false, err
				}
				if !onDisk {
					_, present, err := DecodeProjectDeploymentsEnv(r.graph.env["AI_PROJECT_DEPLOYMENTS"])
					if err != nil {
						return false, err
					}
					if !present {
						return false, nil
					}
				}
			}
		}
	}
	return true, nil
}

func hasPendingModelOrProjectProvision(raw string) bool {
	for part := range strings.SplitSeq(raw, ",") {
		switch strings.TrimSpace(part) {
		case "model_deployment", "project":
			return true
		}
	}
	return false
}

func (provisionedDeploymentResolver) Create(_ context.Context, modelName string) error {
	return exterrors.Dependency(
		exterrors.CodeModelDeploymentNotFound,
		fmt.Sprintf("model deployment %q has not been provisioned yet", modelName),
		"run `azd provision` or `azd up` to create the model deployment before deploying the agent",
	)
}
