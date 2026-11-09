// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"strings"

	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/foundrypolicy"
	"azureaiagent/internal/project"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/google/uuid"
)

// initPolicyEvaluateCandidate is the evaluation hook used by init policy preflight warnings.
// Tests may override it to verify non-blocking warning output deterministically.
var initPolicyEvaluateCandidate = project.CheckCandidateWithCredential

// initPolicyOut is the writer used for init policy warnings (defaults to os.Stdout).
var initPolicyOut io.Writer = os.Stdout

func foundryProjectResourceID(proj *FoundryProjectInfo) string {
	if proj == nil {
		return ""
	}
	if id := strings.TrimSpace(proj.ResourceId); id != "" {
		return id
	}
	if proj.SubscriptionId == "" || proj.ResourceGroupName == "" ||
		proj.AccountName == "" || proj.ProjectName == "" {
		return ""
	}
	return fmt.Sprintf(
		"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.CognitiveServices/accounts/%s/projects/%s",
		proj.SubscriptionId,
		proj.ResourceGroupName,
		proj.AccountName,
		proj.ProjectName,
	)
}

func canEvaluateInitProjectPolicy(projectID string, cred azcore.TokenCredential) bool {
	if cred == nil {
		return false
	}
	parsed, ok := foundrypolicy.ParseFoundryProjectID(projectID)
	if !ok {
		return false
	}
	return uuid.Validate(parsed.SubscriptionID) == nil
}

func emitInitPolicyWarningForCandidate(
	ctx context.Context,
	cred azcore.TokenCredential,
	candidate foundrypolicy.Candidate,
) {
	if !canEvaluateInitProjectPolicy(candidate.ProjectResourceID, cred) {
		return
	}
	res, err := initPolicyEvaluateCandidate(ctx, cred, candidate)
	if err != nil {
		log.Printf("[FoundryPolicy] Init policy check failed (failing open): %v", err)
		return
	}
	if res.Outcome == foundrypolicy.OutcomeBlocked && len(res.Violations) > 0 {
		_, _ = fmt.Fprint(initPolicyOut, "\n"+project.FormatPolicyViolationsWarning(res.Violations))
	}
}

func warnInitServicePolicyIfBlocked(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	cred azcore.TokenCredential,
	env *azdext.Environment,
	proj *FoundryProjectInfo,
	svc *azdext.ServiceConfig,
	projectRoot string,
	deployments []project.Deployment,
) {
	projectID := foundryProjectResourceID(proj)
	if !canEvaluateInitProjectPolicy(projectID, cred) || svc == nil {
		return
	}

	azdEnv := map[string]string{
		"AZURE_AI_PROJECT_ID": projectID,
	}
	if azdClient != nil && env != nil && env.Name != "" {
		if resp, err := azdClient.Environment().GetValues(ctx, &azdext.GetEnvironmentRequest{
			Name: env.Name,
		}); err == nil && resp != nil {
			for _, kv := range resp.KeyValues {
				azdEnv[kv.Key] = kv.Value
			}
		}
	}
	azdEnv["AZURE_AI_PROJECT_ID"] = projectID

	var projectServices map[string]*azdext.ServiceConfig
	if azdClient != nil {
		if projClient := azdClient.Project(); projClient != nil {
			if projResp, err := projClient.Get(ctx, &azdext.EmptyRequest{}); err == nil &&
				projResp != nil && projResp.Project != nil {
				projectServices = projResp.Project.GetServices()
			}
		}
	}

	candidate := project.ExtractServicePolicyCandidateWithServices(svc, projectServices, projectRoot, azdEnv)
	for _, dep := range deployments {
		candidate.Models = append(candidate.Models, foundrypolicy.ModelCandidate{
			DeploymentName: dep.Name,
			Name:           dep.Model.Name,
			Format:         dep.Model.Format,
			Version:        dep.Model.Version,
		})
	}
	candidate.Models = foundrypolicy.MergeModelCandidates(candidate.Models)

	emitInitPolicyWarningForCandidate(ctx, cred, candidate)
}

func warnInitPromptPolicyIfBlocked(
	ctx context.Context,
	cred azcore.TokenCredential,
	proj *FoundryProjectInfo,
	promptAgent *agent_yaml.PromptAgent,
	deployment *project.Deployment,
) {
	projectID := foundryProjectResourceID(proj)
	if !canEvaluateInitProjectPolicy(projectID, cred) || promptAgent == nil {
		return
	}

	props, err := project.PromptAgentDefinitionToServiceProperties(*promptAgent)
	if err != nil {
		return
	}
	svc := &azdext.ServiceConfig{
		Name:                 promptAgent.Name,
		Host:                 AiAgentHost,
		AdditionalProperties: props,
	}
	var deployments []project.Deployment
	if deployment != nil {
		deployments = []project.Deployment{*deployment}
	}
	warnInitServicePolicyIfBlocked(ctx, nil, cred, nil, proj, svc, ".", deployments)
}

func warnInitAdoptedServicesPolicyIfBlocked(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	cred azcore.TokenCredential,
	env *azdext.Environment,
	proj *FoundryProjectInfo,
	projectRoot string,
) {
	projectID := foundryProjectResourceID(proj)
	if !canEvaluateInitProjectPolicy(projectID, cred) || azdClient == nil {
		return
	}
	projResp, err := azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil || projResp == nil || projResp.Project == nil {
		return
	}
	for _, svc := range projResp.Project.Services {
		if svc != nil && svc.GetHost() == AiAgentHost {
			warnInitServicePolicyIfBlocked(ctx, azdClient, cred, env, proj, svc, projectRoot, nil)
		}
	}
}
