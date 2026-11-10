// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"maps"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/pkg/agents/agent_yaml"
	"azureaiagent/internal/pkg/agents/agentkind"
	"azureaiagent/internal/pkg/envkey"
	"azureaiagent/internal/pkg/foundrypolicy"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/google/uuid"
)

// skipPolicyPreflightEnvKey allows opting out of client-side Azure Policy preflight checks.
// Accepted values follow strconv.ParseBool ("1", "t", "T", "true", "TRUE", "True"); defaults
// to false. Values set in the active azd environment take precedence over the process
// environment. Microsoft Foundry still enforces Azure Policy assignments server-side.
const (
	skipPolicyPreflightEnvKey       = "AZD_AI_AGENT_SKIP_POLICY_CHECKS"
	legacySkipPolicyPreflightEnvKey = "AZD_AGENT_SKIP_POLICY_CHECKS"
)

type policyPreflightOperations struct {
	loadInventory func(ctx context.Context, cred azcore.TokenCredential, projectID string) (foundrypolicy.Inventory, error)
	evaluateModel func(
		ctx context.Context,
		cred azcore.TokenCredential,
		projectID string,
		models []foundrypolicy.ModelCandidate,
	) (foundrypolicy.ModelPolicyEvaluation, error)
	evalCache *candidateEvaluationCache
}

func defaultPolicyPreflightOperations() policyPreflightOperations {
	return policyPreflightOperations{
		loadInventory: loadProjectPolicyInventory,
		evaluateModel: evaluateProjectModelPolicies,
		evalCache:     defaultCandidateEvalCache,
	}
}

type candidateEvalCacheEntry struct {
	result   foundrypolicy.EvaluationResult
	cachedAt time.Time
}

type candidateEvaluationCache struct {
	mu      sync.Mutex
	entries map[string]candidateEvalCacheEntry
	ttl     time.Duration
	now     func() time.Time
}

func newCandidateEvaluationCache(ttl time.Duration, now func() time.Time) *candidateEvaluationCache {
	if ttl <= 0 {
		ttl = foundrypolicy.DefaultPolicyCacheTTL
	}
	if now == nil {
		now = time.Now
	}
	return &candidateEvaluationCache{
		entries: map[string]candidateEvalCacheEntry{},
		ttl:     ttl,
		now:     now,
	}
}

var defaultCandidateEvalCache = newCandidateEvaluationCache(
	foundrypolicy.DefaultPolicyCacheTTL,
	time.Now,
)

type policyAssignmentCacheFingerprint struct {
	Assignment            foundrypolicy.PolicyAssignment `json:"assignment"`
	ManagementGroupScopes []string                       `json:"managementGroupScopes,omitempty"`
	IsExempt              bool                           `json:"isExempt,omitempty"`
}

func candidateCacheKey(candidate foundrypolicy.Candidate, inv foundrypolicy.Inventory) (string, bool) {
	normProj := foundrypolicy.NormalizeScope(candidate.ProjectResourceID)
	if normProj == "" {
		return "", false
	}
	normalizedCandidate := candidate
	normalizedCandidate.ProjectResourceID = normProj

	fingerprints := make([]policyAssignmentCacheFingerprint, 0, len(inv.Assignments))
	for _, a := range inv.Assignments {
		fingerprints = append(fingerprints, policyAssignmentCacheFingerprint{
			Assignment:            a,
			ManagementGroupScopes: a.ManagementGroupScopes,
			IsExempt:              a.IsExempt,
		})
	}

	payload := struct {
		Candidate   foundrypolicy.Candidate            `json:"candidate"`
		ProjectID   string                             `json:"projectId"`
		Assignments []policyAssignmentCacheFingerprint `json:"assignments"`
	}{
		Candidate:   normalizedCandidate,
		ProjectID:   foundrypolicy.NormalizeScope(inv.ProjectID),
		Assignments: fingerprints,
	}
	b, err := json.Marshal(payload)
	if err != nil {
		return "", false
	}
	return normProj + "|" + string(b), true
}

func (c *candidateEvaluationCache) get(
	candidate foundrypolicy.Candidate,
	inv foundrypolicy.Inventory,
) (foundrypolicy.EvaluationResult, bool) {
	if c == nil {
		return foundrypolicy.EvaluationResult{}, false
	}
	key, ok := candidateCacheKey(candidate, inv)
	if !ok {
		return foundrypolicy.EvaluationResult{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, exists := c.entries[key]
	if !exists || c.now().Sub(entry.cachedAt) >= c.ttl {
		return foundrypolicy.EvaluationResult{}, false
	}
	return entry.result, true
}

func (c *candidateEvaluationCache) put(
	candidate foundrypolicy.Candidate,
	inv foundrypolicy.Inventory,
	res foundrypolicy.EvaluationResult,
) {
	if c == nil || res.IncompleteReason != "" || res.Outcome == foundrypolicy.OutcomeIndeterminate {
		return
	}
	key, ok := candidateCacheKey(candidate, inv)
	if !ok {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = candidateEvalCacheEntry{
		result:   res,
		cachedAt: c.now(),
	}
}

func loadProjectPolicyInventoryFromCache(
	ctx context.Context,
	cache *foundrypolicy.InventoryCache,
	projectID string,
	fetch func(context.Context) (foundrypolicy.Inventory, error),
) (foundrypolicy.Inventory, error) {
	if cache == nil {
		cache = foundrypolicy.DefaultCache
	}
	res, err := cache.GetFresh(ctx, projectID, fetch)
	if err != nil {
		return foundrypolicy.Inventory{}, err
	}
	if res.Freshness == foundrypolicy.FreshnessStale {
		return foundrypolicy.Inventory{}, errors.New("policy inventory cache is stale and refresh failed")
	}
	return res.Inventory, nil
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
	return loadProjectPolicyInventoryFromCache(
		ctx,
		foundrypolicy.DefaultCache,
		projectID,
		func(fetchCtx context.Context) (foundrypolicy.Inventory, error) {
			return foundrypolicy.LoadInventory(fetchCtx, projectID, parsed.SubscriptionID, queryFn)
		},
	)
}

func evaluateProjectModelPolicies(
	ctx context.Context,
	cred azcore.TokenCredential,
	projectID string,
	models []foundrypolicy.ModelCandidate,
) (foundrypolicy.ModelPolicyEvaluation, error) {
	if len(models) == 0 {
		return foundrypolicy.ModelPolicyEvaluation{}, nil
	}
	pipeline := foundrypolicy.NewARMPipeline(cred)
	evaluator := foundrypolicy.NewARMDeploymentPolicyEvaluator(pipeline, "")
	return evaluator.EvaluateModelDeployments(ctx, projectID, models)
}

type funcDeploymentPolicyEvaluator struct {
	fn func(
		ctx context.Context,
		projectResourceID string,
		models []foundrypolicy.ModelCandidate,
	) (foundrypolicy.ModelPolicyEvaluation, error)
}

func (f funcDeploymentPolicyEvaluator) EvaluateModelDeployments(
	ctx context.Context,
	projectResourceID string,
	models []foundrypolicy.ModelCandidate,
) (foundrypolicy.ModelPolicyEvaluation, error) {
	if f.fn == nil {
		return foundrypolicy.ModelPolicyEvaluation{}, nil
	}
	return f.fn(ctx, projectResourceID, models)
}

func expandPolicyEnv(value string, azdEnv map[string]string) string {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return ""
	}
	expanded, err := ExpandEnv(trimmed, func(name string) string {
		if v, ok := azdEnv[name]; ok {
			return v
		}
		v, _ := os.LookupEnv(name)
		return v
	})
	if err != nil {
		return ""
	}
	expanded = strings.TrimSpace(expanded)
	if strings.Contains(expanded, "${") {
		return ""
	}
	return expanded
}

func resolvePromptModelForPolicy(
	rawModel string,
	svc *azdext.ServiceConfig,
	projectServices map[string]*azdext.ServiceConfig,
	projectRoot string,
	azdEnv map[string]string,
) string {
	trimmed := strings.TrimSpace(rawModel)
	if trimmed == "" {
		return ""
	}
	if resolved, err := ResolvePromptAgentModel(trimmed, svc, projectServices, projectRoot, azdEnv); err == nil {
		resolved = strings.TrimSpace(resolved)
		if resolved != "" && !strings.Contains(resolved, "${") {
			return resolved
		}
	}
	return expandPolicyEnv(trimmed, azdEnv)
}

// ExtractServicePolicyCandidate inspects an azure.ai.agent service configuration and
// extracts its policy-relevant attributes (agent kind, container image or registry,
// models, and tools).
func ExtractServicePolicyCandidate(
	svc *azdext.ServiceConfig,
	projectRoot string,
	azdEnv map[string]string,
) foundrypolicy.Candidate {
	return ExtractServicePolicyCandidateWithServices(svc, nil, projectRoot, azdEnv)
}

// ExtractServicePolicyCandidateWithServices inspects an azure.ai.agent service configuration
// along with sibling project services (for resolving azure.ai.project deployment bindings) and
// extracts its policy-relevant attributes.
func ExtractServicePolicyCandidateWithServices(
	svc *azdext.ServiceConfig,
	projectServices map[string]*azdext.ServiceConfig,
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
		candidate.Models = foundrypolicy.MergeModelCandidates(append(candidate.Models, m))
	}

	addTool := func(t foundrypolicy.ToolCandidate) {
		typ := strings.TrimSpace(t.Type)
		if typ == "" {
			return
		}
		for _, existing := range candidate.Tools {
			if strings.EqualFold(existing.Type, typ) &&
				strings.EqualFold(existing.Name, t.Name) &&
				strings.EqualFold(existing.ConnectionID, t.ConnectionID) &&
				strings.EqualFold(existing.ServerURL, t.ServerURL) &&
				strings.EqualFold(existing.ConnectorID, t.ConnectorID) {
				return
			}
		}
		candidate.Tools = append(candidate.Tools, t)
	}

	hasToolOfType := func(toolType string) bool {
		for _, existing := range candidate.Tools {
			if strings.EqualFold(existing.Type, toolType) {
				return true
			}
		}
		return false
	}

	toolEnv := promptToolEnvironment(azdEnv, svc.GetEnvironment())

	if pa, found, promptErr := PromptAgentFromResolvedService(svc, projectRoot); promptErr == nil && found {
		if candidate.AgentKind == "" {
			candidate.AgentKind = string(agent_yaml.AgentKindPrompt)
		}
		if model := resolvePromptModelForPolicy(pa.Model, svc, projectServices, projectRoot, azdEnv); model != "" {
			addModel(foundrypolicy.ModelCandidate{
				DeploymentName: model,
			})
		}
		for _, rawTool := range pa.Tools {
			for _, tc := range extractToolCandidatesFromAny(rawTool, toolEnv) {
				addTool(tc)
			}
		}
		for _, tb := range pa.Toolboxes {
			if tbName := expandPolicyEnv(tb.Name, toolEnv); tbName != "" {
				tbURL := expandPolicyEnv(toolEnv[envkey.ToolboxMCPEndpoint(tbName)], toolEnv)
				addTool(foundrypolicy.ToolCandidate{
					Type:      "mcp",
					Name:      "toolbox:" + tbName,
					ServerURL: tbURL,
				})
			}
		}
		if pa.Memory != nil {
			if chat := resolvePromptModelForPolicy(pa.Memory.ChatModel, svc, projectServices, projectRoot, azdEnv); chat != "" {
				addModel(foundrypolicy.ModelCandidate{
					DeploymentName: chat,
				})
			}
			if emb := resolvePromptModelForPolicy(pa.Memory.EmbeddingModel, svc, projectServices, projectRoot, azdEnv); emb != "" {
				addModel(foundrypolicy.ModelCandidate{
					DeploymentName: emb,
				})
			}
			if !hasToolOfType(memorySearchToolType) {
				addTool(foundrypolicy.ToolCandidate{Type: memorySearchToolType})
			}
		}
	} else if va, isVoice, voiceErr := resolveVoiceAgentForDeploy(svc, projectRoot); voiceErr == nil && isVoice {
		if candidate.AgentKind == "" {
			candidate.AgentKind = "voice"
		}
		if va.Model != nil {
			if modelID := expandPolicyEnv(va.Model.Id, toolEnv); modelID != "" {
				addModel(foundrypolicy.ModelCandidate{
					DeploymentName: modelID,
				})
			}
		}
		for _, rawTool := range va.Tools {
			for _, tc := range extractToolCandidatesFromAny(rawTool, toolEnv) {
				addTool(tc)
			}
		}
	} else if ca, isHosted, _, hostedErr := LoadHostedAgentDefinition(svc, projectRoot); hostedErr == nil && isHosted {
		if candidate.AgentKind == "" {
			candidate.AgentKind = string(agent_yaml.AgentKindHosted)
		}
		if ca.CodeConfiguration == nil {
			image := expandPolicyEnv(ca.Image, toolEnv)
			skipACR := strings.EqualFold(strings.TrimSpace(azdEnv["AZD_AGENT_SKIP_ACR"]), "true")
			if strings.TrimSpace(ca.RegistryConnectionID) != "" ||
				svc.GetDocker().GetImagePassthrough() ||
				(image != "" && skipACR) {
				candidate.ContainerImage = image
			} else if image == "" {
				acrEndpoint := strings.TrimSpace(azdEnv["AZURE_CONTAINER_REGISTRY_ENDPOINT"])
				if acrEndpoint == "" {
					acrEndpoint = expandPolicyEnv(svc.GetDocker().GetRegistry(), toolEnv)
				}
				if acrEndpoint != "" {
					candidate.ContainerRegistry = acrEndpoint
				}
			}
		}
	}

	isHostedAgent := strings.EqualFold(candidate.AgentKind, string(agent_yaml.AgentKindHosted))

	if targetCfg, cfgErr := LoadServiceTargetAgentConfig(svc); cfgErr == nil && targetCfg != nil {
		for _, dep := range targetCfg.Deployments {
			addModel(foundrypolicy.ModelCandidate{
				DeploymentName: expandPolicyEnv(dep.Name, toolEnv),
				Name:           expandPolicyEnv(dep.Model.Name, toolEnv),
				Format:         expandPolicyEnv(dep.Model.Format, toolEnv),
				Version:        expandPolicyEnv(dep.Model.Version, toolEnv),
			})
		}
		for _, res := range targetCfg.Resources {
			if strings.EqualFold(strings.TrimSpace(res.Resource), "model") {
				if depName := expandPolicyEnv(res.ConnectionName, toolEnv); depName != "" {
					addModel(foundrypolicy.ModelCandidate{
						DeploymentName: depName,
					})
				}
			}
		}
		for _, ms := range targetCfg.MemoryStores {
			if chat := expandPolicyEnv(ms.ChatModel, toolEnv); chat != "" {
				addModel(foundrypolicy.ModelCandidate{DeploymentName: chat})
			}
			if emb := expandPolicyEnv(ms.EmbeddingModel, toolEnv); emb != "" {
				addModel(foundrypolicy.ModelCandidate{DeploymentName: emb})
			}
			storeName := expandPolicyEnv(ms.Name, toolEnv)
			if !isHostedAgent && (!hasToolOfType(memorySearchToolType) || storeName != "") {
				addTool(foundrypolicy.ToolCandidate{Type: memorySearchToolType, Name: storeName})
			}
		}
		for _, tb := range targetCfg.Toolboxes {
			if !isHostedAgent {
				if tbName := expandPolicyEnv(tb.Name, toolEnv); tbName != "" {
					tbURL := expandPolicyEnv(toolEnv[envkey.ToolboxMCPEndpoint(tbName)], toolEnv)
					addTool(foundrypolicy.ToolCandidate{
						Type:      "mcp",
						Name:      "toolbox:" + tbName,
						ServerURL: tbURL,
					})
				}
			}
			for _, rawTool := range tb.Tools {
				for _, tc := range extractToolCandidatesFromAny(rawTool, toolEnv) {
					addTool(tc)
				}
			}
		}

		connTargets := map[string]string{}
		for _, c := range targetCfg.Connections {
			if name := expandPolicyEnv(c.Name, toolEnv); name != "" {
				if target := expandPolicyEnv(c.Target, toolEnv); target != "" {
					connTargets[strings.ToLower(name)] = target
				}
			}
		}
		for _, c := range targetCfg.ToolConnections {
			if name := expandPolicyEnv(c.Name, toolEnv); name != "" {
				if target := expandPolicyEnv(c.Target, toolEnv); target != "" {
					connTargets[strings.ToLower(name)] = target
				}
			}
		}
		if len(connTargets) > 0 {
			for i := range candidate.Tools {
				if strings.EqualFold(candidate.Tools[i].Type, "mcp") &&
					strings.TrimSpace(candidate.Tools[i].ServerURL) == "" &&
					strings.TrimSpace(candidate.Tools[i].ConnectionID) != "" {
					if target, ok := connTargets[strings.ToLower(strings.TrimSpace(candidate.Tools[i].ConnectionID))]; ok {
						candidate.Tools[i].ServerURL = target
					}
				}
			}
		}
	}

	if len(candidate.Models) > 0 {
		if envDeps, present, decErr := DecodeProjectDeploymentsEnv(azdEnv["AI_PROJECT_DEPLOYMENTS"]); present && decErr == nil {
			for _, dep := range envDeps {
				depName := expandPolicyEnv(dep.Name, azdEnv)
				if depName == "" {
					continue
				}
				matched := false
				for _, existing := range candidate.Models {
					if strings.EqualFold(existing.DeploymentName, depName) ||
						(existing.DeploymentName == "" && strings.EqualFold(existing.Name, depName)) {
						matched = true
						break
					}
				}
				if !matched {
					continue
				}
				envCandidate := foundrypolicy.ModelCandidate{
					DeploymentName: depName,
					Name:           expandPolicyEnv(dep.Model.Name, azdEnv),
					Format:         expandPolicyEnv(dep.Model.Format, azdEnv),
					Version:        expandPolicyEnv(dep.Model.Version, azdEnv),
				}
				candidate.Models = foundrypolicy.MergeModelCandidates(
					append([]foundrypolicy.ModelCandidate{envCandidate}, candidate.Models...),
				)
			}
		}
	}

	return candidate
}

func extractNestedConnectionIDs(raw any, azdEnv map[string]string, out *[]string) {
	switch typed := raw.(type) {
	case map[string]any:
		keys := slices.Sorted(maps.Keys(typed))
		for _, k := range keys {
			v := typed[k]
			switch strings.ToLower(strings.TrimSpace(k)) {
			case "project_connection_id", "connection_id", "connection":
				if str, ok := v.(string); ok {
					conn := expandPolicyEnv(str, azdEnv)
					if conn != "" && !slices.ContainsFunc(*out, func(existing string) bool {
						return strings.EqualFold(existing, conn)
					}) {
						*out = append(*out, conn)
					}
					continue
				}
			}
			extractNestedConnectionIDs(v, azdEnv, out)
		}
	case []any:
		for _, item := range typed {
			extractNestedConnectionIDs(item, azdEnv, out)
		}
	}
}

func extractToolCandidatesFromAny(raw any, azdEnv map[string]string) []foundrypolicy.ToolCandidate {
	switch typed := raw.(type) {
	case string:
		s := expandPolicyEnv(typed, azdEnv)
		if s != "" {
			return []foundrypolicy.ToolCandidate{{Type: s}}
		}
	case map[string]any:
		rawType, _ := typed["type"].(string)
		if rawType == "" {
			rawType, _ = typed["kind"].(string)
		}
		toolType := expandPolicyEnv(rawType, azdEnv)
		if toolType == "" {
			return nil
		}
		rawName, _ := typed["name"].(string)
		name := expandPolicyEnv(rawName, azdEnv)

		rawServerURL, _ := typed["server_url"].(string)
		if rawServerURL == "" {
			rawServerURL, _ = typed["serverUrl"].(string)
		}
		serverURL := expandPolicyEnv(rawServerURL, azdEnv)

		rawConnectorID, _ := typed["connector_id"].(string)
		if rawConnectorID == "" {
			rawConnectorID, _ = typed["connectorId"].(string)
		}
		connectorID := expandPolicyEnv(rawConnectorID, azdEnv)

		var connIDs []string
		extractNestedConnectionIDs(typed, azdEnv, &connIDs)
		if len(connIDs) == 0 {
			return []foundrypolicy.ToolCandidate{{
				Type:        toolType,
				Name:        name,
				ServerURL:   serverURL,
				ConnectorID: connectorID,
			}}
		}
		out := make([]foundrypolicy.ToolCandidate, 0, len(connIDs))
		for _, connID := range connIDs {
			out = append(out, foundrypolicy.ToolCandidate{
				Type:         toolType,
				Name:         name,
				ConnectionID: connID,
				ServerURL:    serverURL,
				ConnectorID:  connectorID,
			})
		}
		return out
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

	var projectServices map[string]*azdext.ServiceConfig
	if projClient := azdClient.Project(); projClient != nil {
		if projResp, projErr := projClient.Get(ctx, &azdext.EmptyRequest{}); projErr == nil &&
			projResp != nil && projResp.Project != nil {
			projectServices = projResp.Project.GetServices()
			if strings.TrimSpace(projectRoot) == "" {
				projectRoot = projResp.Project.GetPath()
			}
		}
	}

	candidate := ExtractServicePolicyCandidateWithServices(svc, projectServices, projectRoot, azdEnv)

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
	azdEnv map[string]string,
) error {
	return checkHostedContainerImagePolicy(
		ctx,
		projectResourceID,
		cred,
		serviceName,
		imageURL,
		azdEnv,
		defaultPolicyPreflightOperations(),
	)
}

func checkHostedContainerImagePolicy(
	ctx context.Context,
	projectResourceID string,
	cred azcore.TokenCredential,
	serviceName string,
	imageURL string,
	azdEnv map[string]string,
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
	if shouldSkipPolicyChecks(azdEnv) {
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
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > foundrypolicy.DefaultPolicyPreflightTimeout {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, foundrypolicy.DefaultPolicyPreflightTimeout)
		defer cancel()
	}

	if ops.loadInventory == nil {
		ops = defaultPolicyPreflightOperations()
	}

	inv, err := ops.loadInventory(ctx, cred, candidate.ProjectResourceID)
	if err != nil {
		return foundrypolicy.EvaluationResult{Outcome: foundrypolicy.OutcomeAllowed}, err
	}

	if cachedRes, hit := ops.evalCache.get(candidate, inv); hit {
		return cachedRes, nil
	}

	var modelEval foundrypolicy.DeploymentPolicyEvaluator
	if ops.evaluateModel != nil && cred != nil {
		modelEval = funcDeploymentPolicyEvaluator{
			fn: func(
				evalCtx context.Context,
				projectID string,
				models []foundrypolicy.ModelCandidate,
			) (foundrypolicy.ModelPolicyEvaluation, error) {
				return ops.evaluateModel(evalCtx, cred, projectID, models)
			},
		}
	}

	res := foundrypolicy.EvaluateCandidate(ctx, inv, candidate, modelEval)
	ops.evalCache.put(candidate, inv, res)
	return res, nil
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
	keys := []string{skipPolicyPreflightEnvKey, legacySkipPolicyPreflightEnvKey}
	for _, key := range keys {
		if val := strings.TrimSpace(azdEnv[key]); val != "" {
			if skip, err := strconv.ParseBool(val); err == nil {
				return skip
			}
		}
	}
	for _, key := range keys {
		if val := strings.TrimSpace(os.Getenv(key)); val != "" {
			if skip, err := strconv.ParseBool(val); err == nil {
				return skip
			}
		}
	}
	return false
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
	ServiceName      string
	AgentKind        string
	Outcome          foundrypolicy.EvaluationOutcome
	Violations       []foundrypolicy.Violation
	IncompleteReason string
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
		candidate := ExtractServicePolicyCandidateWithServices(
			svc,
			projResp.Project.GetServices(),
			projResp.Project.GetPath(),
			azdEnv,
		)
		candidate.ProjectResourceID = info.ProjectScope
		evalRes, evalErr := EvaluateCandidateWithCredential(ctx, cred, candidate, ops)
		if evalErr != nil {
			return nil, fmt.Errorf("evaluate policy for service %q: %w", name, evalErr)
		}
		findings = append(findings, ServicePolicyFinding{
			ServiceName:      name,
			AgentKind:        candidate.AgentKind,
			Outcome:          evalRes.Outcome,
			Violations:       evalRes.Violations,
			IncompleteReason: evalRes.IncompleteReason,
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
