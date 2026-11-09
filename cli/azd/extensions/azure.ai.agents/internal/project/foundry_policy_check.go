// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"cmp"
	"context"
	"fmt"
	"log"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/agents/agentkind"
	"azureaiagent/internal/pkg/foundrypolicy"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/google/uuid"
)

// skipPolicyPreflightEnvKey allows opting out of client-side Azure Policy preflight checks.
const skipPolicyPreflightEnvKey = "AZD_AGENT_SKIP_POLICY_CHECKS"

type policyPreflightOperations struct {
	loadInventory func(ctx context.Context, cred azcore.TokenCredential, projectID string) (foundrypolicy.Inventory, error)
	evaluateModel func(ctx context.Context, cred azcore.TokenCredential, projectID string, models []foundrypolicy.ModelCandidate) ([]foundrypolicy.Violation, error)
}

func defaultPolicyPreflightOperations() policyPreflightOperations {
	return policyPreflightOperations{
		loadInventory: loadProjectPolicyInventory,
		evaluateModel: evaluateProjectModelPolicies,
	}
}

func loadProjectPolicyInventory(
	ctx context.Context,
	cred azcore.TokenCredential,
	projectID string,
) (foundrypolicy.Inventory, error) {
	parsed, ok := foundrypolicy.ParseFoundryProjectID(projectID)
	if !ok {
		return foundrypolicy.Inventory{}, fmt.Errorf("invalid Foundry project resource ID: %s", projectID)
	}
	pipeline := foundrypolicy.NewARMPipeline(cred)
	queryFn := foundrypolicy.NewResourceGraphQueryFunc(pipeline, "", nil)
	return foundrypolicy.DefaultCache.Get(ctx, projectID, func(fetchCtx context.Context) (foundrypolicy.Inventory, error) {
		return foundrypolicy.LoadInventory(fetchCtx, projectID, parsed.SubscriptionID, queryFn)
	})
}

func evaluateProjectModelPolicies(
	ctx context.Context,
	cred azcore.TokenCredential,
	projectID string,
	models []foundrypolicy.ModelCandidate,
) ([]foundrypolicy.Violation, error) {
	if len(models) == 0 {
		return nil, nil
	}
	pipeline := foundrypolicy.NewARMPipeline(cred)
	evaluator := foundrypolicy.NewARMDeploymentPolicyEvaluator(pipeline, "")
	return evaluator.EvaluateModelDeployments(ctx, projectID, models)
}

type funcDeploymentPolicyEvaluator struct {
	fn func(ctx context.Context, projectResourceID string, models []foundrypolicy.ModelCandidate) ([]foundrypolicy.Violation, error)
}

func (f funcDeploymentPolicyEvaluator) EvaluateModelDeployments(
	ctx context.Context,
	projectResourceID string,
	models []foundrypolicy.ModelCandidate,
) ([]foundrypolicy.Violation, error) {
	if f.fn == nil {
		return nil, nil
	}
	return f.fn(ctx, projectResourceID, models)
}

// ExtractServicePolicyCandidate inspects an azure.ai.agent service configuration and
// extracts its policy-relevant attributes (agent kind, container image or registry,
// models, and tools).
func ExtractServicePolicyCandidate(
	svc *azdext.ServiceConfig,
	projectRoot string,
	azdEnv map[string]string,
) foundrypolicy.Candidate {
	if svc == nil {
		return foundrypolicy.Candidate{}
	}

	candidate := foundrypolicy.Candidate{
		ProjectResourceID: strings.TrimSpace(azdEnv["AZURE_AI_PROJECT_ID"]),
		ServiceName:       svc.GetName(),
	}

	kind, err := agentkind.Kind(svc, projectRoot)
	if err == nil && kind != "" {
		candidate.AgentKind = foundrypolicy.NormalizeAgentKindForPolicy(kind)
	}

	addModel := func(m foundrypolicy.ModelCandidate) {
		dep := strings.TrimSpace(m.DeploymentName)
		name := strings.TrimSpace(m.Name)
		if dep == "" && name == "" {
			return
		}
		for _, existing := range candidate.Models {
			if strings.EqualFold(existing.DeploymentName, dep) &&
				strings.EqualFold(existing.Name, name) {
				return
			}
		}
		candidate.Models = append(candidate.Models, m)
	}

	addTool := func(t foundrypolicy.ToolCandidate) {
		typ := strings.TrimSpace(t.Type)
		if typ == "" {
			return
		}
		for _, existing := range candidate.Tools {
			if strings.EqualFold(existing.Type, typ) &&
				strings.EqualFold(existing.Name, t.Name) &&
				strings.EqualFold(existing.ConnectionID, t.ConnectionID) {
				return
			}
		}
		candidate.Tools = append(candidate.Tools, t)
	}

	if pa, found, promptErr := PromptAgentFromResolvedService(svc, projectRoot); promptErr == nil && found {
		if candidate.AgentKind == "" {
			candidate.AgentKind = string(agent_yaml.AgentKindPrompt)
		}
		if model := strings.TrimSpace(pa.Model); model != "" && !strings.Contains(model, "${") {
			addModel(foundrypolicy.ModelCandidate{
				DeploymentName: model,
				Name:           model,
			})
		}
		for _, rawTool := range pa.Tools {
			for _, tc := range extractToolCandidatesFromAny(rawTool) {
				addTool(tc)
			}
		}
		if pa.Memory != nil {
			addTool(foundrypolicy.ToolCandidate{Type: "memory_search"})
		}
	} else if va, isVoice, voiceErr := resolveVoiceAgentForDeploy(svc, projectRoot); voiceErr == nil && isVoice {
		if candidate.AgentKind == "" {
			candidate.AgentKind = "voice"
		}
		if va.Model != nil {
			if modelID := strings.TrimSpace(va.Model.Id); modelID != "" && !strings.Contains(modelID, "${") {
				addModel(foundrypolicy.ModelCandidate{
					DeploymentName: modelID,
					Name:           modelID,
				})
			}
		}
		for _, rawTool := range va.Tools {
			for _, tc := range extractToolCandidatesFromAny(rawTool) {
				addTool(tc)
			}
		}
	} else if ca, isHosted, _, hostedErr := LoadHostedAgentDefinition(svc, projectRoot); hostedErr == nil && isHosted {
		if candidate.AgentKind == "" {
			candidate.AgentKind = string(agent_yaml.AgentKindHosted)
		}
		if ca.CodeConfiguration == nil {
			image := strings.TrimSpace(ca.Image)
			skipACR := strings.EqualFold(strings.TrimSpace(azdEnv["AZD_AGENT_SKIP_ACR"]), "true")
			if strings.TrimSpace(ca.RegistryConnectionID) != "" ||
				svc.GetDocker().GetImagePassthrough() ||
				(image != "" && skipACR) {
				candidate.ContainerImage = image
			} else if acrEndpoint := strings.TrimSpace(azdEnv["AZURE_CONTAINER_REGISTRY_ENDPOINT"]); acrEndpoint != "" {
				candidate.ContainerRegistry = acrEndpoint
			}
		}
	}

	if targetCfg, cfgErr := LoadServiceTargetAgentConfig(svc); cfgErr == nil && targetCfg != nil {
		for _, dep := range targetCfg.Deployments {
			addModel(foundrypolicy.ModelCandidate{
				DeploymentName: dep.Name,
				Name:           dep.Model.Name,
				Format:         dep.Model.Format,
				Version:        dep.Model.Version,
			})
		}
		for _, ms := range targetCfg.MemoryStores {
			if chat := strings.TrimSpace(ms.ChatModel); chat != "" && !strings.Contains(chat, "${") {
				addModel(foundrypolicy.ModelCandidate{DeploymentName: chat, Name: chat})
			}
			if emb := strings.TrimSpace(ms.EmbeddingModel); emb != "" && !strings.Contains(emb, "${") {
				addModel(foundrypolicy.ModelCandidate{DeploymentName: emb, Name: emb})
			}
			addTool(foundrypolicy.ToolCandidate{Type: "memory_search", Name: ms.Name})
		}
		for _, tb := range targetCfg.Toolboxes {
			if strings.TrimSpace(tb.Name) != "" {
				addTool(foundrypolicy.ToolCandidate{Type: "mcp", Name: "toolbox:" + strings.TrimSpace(tb.Name)})
			}
			for _, rawTool := range tb.Tools {
				for _, tc := range extractToolCandidatesFromAny(rawTool) {
					addTool(tc)
				}
			}
		}
	}

	return candidate
}

func extractToolCandidatesFromAny(raw any) []foundrypolicy.ToolCandidate {
	switch typed := raw.(type) {
	case string:
		s := strings.TrimSpace(typed)
		if s != "" {
			return []foundrypolicy.ToolCandidate{{Type: s}}
		}
	case map[string]any:
		toolType, _ := typed["type"].(string)
		if toolType == "" {
			toolType, _ = typed["kind"].(string)
		}
		name, _ := typed["name"].(string)
		connID, _ := typed["project_connection_id"].(string)
		if connID == "" {
			connID, _ = typed["connection_id"].(string)
		}
		if connID == "" {
			connID, _ = typed["connection"].(string)
		}
		if strings.TrimSpace(toolType) != "" {
			return []foundrypolicy.ToolCandidate{{
				Type:         strings.TrimSpace(toolType),
				Name:         strings.TrimSpace(name),
				ConnectionID: strings.TrimSpace(connID),
			}}
		}
	}
	return nil
}

// CheckServicePolicyForPredeploy runs the best-effort Azure Policy preflight check for
// an agent service during predeploy. It returns a structured exterrors error only when
// a policy assignment is confirmed to block the deployment; query failures fail open.
func CheckServicePolicyForPredeploy(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	svc *azdext.ServiceConfig,
	projectRoot string,
) error {
	return checkServicePolicyForPredeploy(ctx, azdClient, svc, projectRoot, defaultPolicyPreflightOperations())
}

func checkServicePolicyForPredeploy(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	svc *azdext.ServiceConfig,
	projectRoot string,
	ops policyPreflightOperations,
) error {
	if azdClient == nil || svc == nil {
		return nil
	}

	envClient := azdClient.Environment()
	envResp, err := envClient.GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil || envResp == nil || envResp.Environment == nil || envResp.Environment.Name == "" {
		return nil
	}
	envName := envResp.Environment.Name

	valuesResp, err := envClient.GetValues(ctx, &azdext.GetEnvironmentRequest{Name: envName})
	if err != nil || valuesResp == nil {
		return nil
	}
	azdEnv := make(map[string]string, len(valuesResp.KeyValues))
	for _, kv := range valuesResp.KeyValues {
		azdEnv[kv.Key] = kv.Value
	}

	if shouldSkipPolicyChecks(azdEnv) {
		log.Printf("Skipping Azure Policy pre-flight check (%s is set)", skipPolicyPreflightEnvKey)
		return nil
	}

	projectID := strings.TrimSpace(azdEnv["AZURE_AI_PROJECT_ID"])
	parsedProject, ok := foundrypolicy.ParseFoundryProjectID(projectID)
	if !ok || uuid.Validate(parsedProject.SubscriptionID) != nil {
		return nil
	}

	tenantResp, err := azdClient.Account().LookupTenant(ctx, &azdext.LookupTenantRequest{
		SubscriptionId: parsedProject.SubscriptionID,
	})
	if err != nil || tenantResp == nil || strings.TrimSpace(tenantResp.TenantId) == "" {
		return nil
	}

	cred, err := azidentity.NewAzureDeveloperCLICredential(&azidentity.AzureDeveloperCLICredentialOptions{
		TenantID:                   tenantResp.TenantId,
		AdditionallyAllowedTenants: []string{"*"},
	})
	if err != nil {
		log.Printf("[FoundryPolicy] Failed to create credential for policy preflight (failing open): %v", err)
		return nil
	}

	candidate := ExtractServicePolicyCandidate(svc, projectRoot, azdEnv)
	result, err := EvaluateCandidateWithCredential(ctx, cred, candidate, ops)
	if err != nil {
		log.Printf("[FoundryPolicy] Policy preflight query failed for %s (failing open): %v", svc.GetName(), err)
		return nil
	}
	if result.Outcome != foundrypolicy.OutcomeBlocked || len(result.Violations) == 0 {
		return nil
	}

	return PolicyViolationsToError(svc.GetName(), result.Violations)
}

// CheckHostedContainerImagePolicy evaluates a resolved container image URL against the
// project's Azure Policy inventory (used in Deploy after Publish or image selection).
// Fails open when policy inventory cannot be queried.
func CheckHostedContainerImagePolicy(
	ctx context.Context,
	projectResourceID string,
	cred azcore.TokenCredential,
	serviceName string,
	imageURL string,
) error {
	return checkHostedContainerImagePolicy(
		ctx,
		projectResourceID,
		cred,
		serviceName,
		imageURL,
		defaultPolicyPreflightOperations(),
	)
}

func checkHostedContainerImagePolicy(
	ctx context.Context,
	projectResourceID string,
	cred azcore.TokenCredential,
	serviceName string,
	imageURL string,
	ops policyPreflightOperations,
) error {
	projectResourceID = strings.TrimSpace(projectResourceID)
	imageURL = strings.TrimSpace(imageURL)
	if projectResourceID == "" || imageURL == "" || cred == nil {
		return nil
	}
	if _, ok := foundrypolicy.ParseFoundryProjectID(projectResourceID); !ok {
		return nil
	}
	if skip, err := strconv.ParseBool(os.Getenv(skipPolicyPreflightEnvKey)); err == nil && skip {
		return nil
	}

	inv, err := ops.loadInventory(ctx, cred, projectResourceID)
	if err != nil {
		log.Printf("[FoundryPolicy] Container image policy check failed for %s (failing open): %v", serviceName, err)
		return nil
	}

	res := foundrypolicy.EvaluateContainerImagePolicy(inv, foundrypolicy.ContainerImageCandidate{
		Type:  foundrypolicy.CandidateTypeImage,
		Value: imageURL,
	})
	if res.Outcome != foundrypolicy.OutcomeBlocked || len(res.Violations) == 0 {
		return nil
	}
	return PolicyViolationsToError(serviceName, res.Violations)
}

// EvaluateCandidateWithCredential loads the policy inventory for candidate.ProjectResourceID
// and evaluates all candidate fields (agent kind, container image/registry, tools, models).
func EvaluateCandidateWithCredential(
	ctx context.Context,
	cred azcore.TokenCredential,
	candidate foundrypolicy.Candidate,
	ops policyPreflightOperations,
) (foundrypolicy.EvaluationResult, error) {
	if ops.loadInventory == nil {
		ops = defaultPolicyPreflightOperations()
	}
	inv, err := ops.loadInventory(ctx, cred, candidate.ProjectResourceID)
	if err != nil {
		return foundrypolicy.EvaluationResult{Outcome: foundrypolicy.OutcomeAllowed}, err
	}

	var modelEval foundrypolicy.DeploymentPolicyEvaluator
	if ops.evaluateModel != nil && cred != nil {
		modelEval = funcDeploymentPolicyEvaluator{
			fn: func(
				evalCtx context.Context,
				projectID string,
				models []foundrypolicy.ModelCandidate,
			) ([]foundrypolicy.Violation, error) {
				return ops.evaluateModel(evalCtx, cred, projectID, models)
			},
		}
	}

	return foundrypolicy.EvaluateCandidate(ctx, inv, candidate, modelEval), nil
}

// CheckCandidateWithCredential is the exported entrypoint for evaluating a Candidate
// with a TokenCredential (used by init and doctor).
func CheckCandidateWithCredential(
	ctx context.Context,
	cred azcore.TokenCredential,
	candidate foundrypolicy.Candidate,
) (foundrypolicy.EvaluationResult, error) {
	return EvaluateCandidateWithCredential(ctx, cred, candidate, defaultPolicyPreflightOperations())
}

func shouldSkipPolicyChecks(azdEnv map[string]string) bool {
	val := strings.TrimSpace(azdEnv[skipPolicyPreflightEnvKey])
	if val == "" {
		val = strings.TrimSpace(os.Getenv(skipPolicyPreflightEnvKey))
	}
	skip, err := strconv.ParseBool(val)
	return err == nil && skip
}

// PolicyViolationsToError converts one or more policy violations into a structured
// exterrors.Validation error with actionable guidance and Foundry Govern links.
func PolicyViolationsToError(serviceName string, violations []foundrypolicy.Violation) error {
	code := selectPolicyErrorCode(violations)

	var messages []string
	var links []string
	for _, v := range violations {
		msg := strings.TrimSpace(v.Message)
		if msg == "" {
			msg = fmt.Sprintf("Blocked by Azure Policy %q (%s: %s).", v.DisplayName, v.Target, v.TargetValue)
		}
		if !slices.Contains(messages, msg) {
			messages = append(messages, msg)
		}
		if url := strings.TrimSpace(v.PortalURL); url != "" && !slices.Contains(links, url) {
			links = append(links, fmt.Sprintf("  View policy (%s): %s", v.DisplayName, url))
		}
	}

	prefix := "deployment is blocked by Azure Policy"
	if strings.TrimSpace(serviceName) != "" {
		prefix = fmt.Sprintf("deployment of service %q is blocked by Azure Policy", serviceName)
	}
	fullMessage := fmt.Sprintf("%s: %s", prefix, strings.Join(messages, " "))

	suggestion := "Update the agent configuration to comply with your organization's Azure Policy " +
		"assignments, or select a different Microsoft Foundry project."
	if len(links) > 0 {
		suggestion += "\n" + strings.Join(links, "\n")
	}

	return exterrors.Validation(code, fullMessage, suggestion)
}

func selectPolicyErrorCode(violations []foundrypolicy.Violation) string {
	if len(violations) == 0 {
		return exterrors.CodeAgentPolicyBlocked
	}
	firstTarget := violations[0].Target
	for _, v := range violations[1:] {
		if v.Target != firstTarget {
			return exterrors.CodeAgentPolicyBlocked
		}
	}
	switch firstTarget {
	case foundrypolicy.TargetAgentKind:
		return exterrors.CodeAgentKindBlockedByPolicy
	case foundrypolicy.TargetContainerImage, foundrypolicy.TargetContainerRegistry:
		return exterrors.CodeContainerImageBlockedByPolicy
	case foundrypolicy.TargetModel:
		return exterrors.CodeModelBlockedByPolicy
	case foundrypolicy.TargetTool:
		return exterrors.CodeToolBlockedByPolicy
	default:
		return exterrors.CodeAgentPolicyBlocked
	}
}

// FormatPolicyViolationsWarning formats policy violations as a non-blocking warning
// banner for CLI flows such as `azd ai agent init`.
func FormatPolicyViolationsWarning(violations []foundrypolicy.Violation) string {
	if len(violations) == 0 {
		return ""
	}
	var b strings.Builder
	b.WriteString(output.WithWarningFormat("Warning: This agent configuration may be blocked by Azure Policy in the selected project:\n"))
	for _, v := range violations {
		msg := strings.TrimSpace(v.Message)
		if msg == "" {
			msg = fmt.Sprintf("The %q policy blocks %s %q.", v.DisplayName, v.Target, v.TargetValue)
		}
		b.WriteString(fmt.Sprintf("  - %s\n", msg))
		if strings.TrimSpace(v.PortalURL) != "" {
			b.WriteString(fmt.Sprintf("    View policy: %s\n", strings.TrimSpace(v.PortalURL)))
		}
	}
	return b.String()
}

// ServicePolicyFinding describes the policy evaluation outcome for one agent service.
type ServicePolicyFinding struct {
	ServiceName string
	AgentKind   string
	Violations  []foundrypolicy.Violation
}

// ProjectPolicyResult is the side-effect-free outcome of QueryProjectPolicy,
// suitable for diagnostics (`azd ai agent doctor`'s `remote.policy` check).
type ProjectPolicyResult struct {
	ProjectScope string
	AccountName  string
	ProjectName  string
	Services     []ServicePolicyFinding
}

// QueryProjectPolicy evaluates Azure Policy compliance for every azure.ai.agent
// service declared in the project against the given Foundry project resource ID.
func QueryProjectPolicy(
	ctx context.Context,
	azdClient *azdext.AzdClient,
	projectResourceID string,
) (*ProjectPolicyResult, error) {
	info, err := parseAgentIdentityInfo(projectResourceID)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidProjectResourceID, err)
	}

	tenantResp, err := azdClient.Account().LookupTenant(ctx, &azdext.LookupTenantRequest{
		SubscriptionId: info.SubscriptionID,
	})
	if err != nil {
		return nil, fmt.Errorf("lookup tenant: %w", err)
	}

	cred, err := azidentity.NewAzureDeveloperCLICredential(&azidentity.AzureDeveloperCLICredentialOptions{
		TenantID:                   tenantResp.TenantId,
		AdditionallyAllowedTenants: []string{"*"},
	})
	if err != nil {
		return nil, fmt.Errorf("create credential: %w", err)
	}

	azdEnv := map[string]string{
		"AZURE_AI_PROJECT_ID": info.ProjectScope,
	}
	if envResp, envErr := azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{}); envErr == nil &&
		envResp != nil && envResp.Environment != nil && envResp.Environment.Name != "" {
		if valResp, valErr := azdClient.Environment().GetValues(ctx, &azdext.GetEnvironmentRequest{
			Name: envResp.Environment.Name,
		}); valErr == nil && valResp != nil {
			for _, kv := range valResp.KeyValues {
				azdEnv[kv.Key] = kv.Value
			}
		}
	}
	azdEnv["AZURE_AI_PROJECT_ID"] = info.ProjectScope

	projResp, err := azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil || projResp == nil || projResp.Project == nil {
		return nil, fmt.Errorf("read project config: %w", err)
	}

	serviceNames := slices.Sorted(maps.Keys(projResp.Project.Services))
	var findings []ServicePolicyFinding
	ops := defaultPolicyPreflightOperations()

	for _, name := range serviceNames {
		svc := projResp.Project.Services[name]
		if svc == nil || svc.GetHost() != "azure.ai.agent" {
			continue
		}
		candidate := ExtractServicePolicyCandidate(svc, projResp.Project.GetPath(), azdEnv)
		candidate.ProjectResourceID = info.ProjectScope
		evalRes, evalErr := EvaluateCandidateWithCredential(ctx, cred, candidate, ops)
		if evalErr != nil {
			return nil, fmt.Errorf("evaluate policy for service %q: %w", name, evalErr)
		}
		findings = append(findings, ServicePolicyFinding{
			ServiceName: name,
			AgentKind:   candidate.AgentKind,
			Violations:  evalRes.Violations,
		})
	}

	slices.SortFunc(findings, func(a, b ServicePolicyFinding) int {
		return cmp.Compare(a.ServiceName, b.ServiceName)
	})

	return &ProjectPolicyResult{
		ProjectScope: info.ProjectScope,
		AccountName:  info.AccountName,
		ProjectName:  info.ProjectName,
		Services:     findings,
	}, nil
}
