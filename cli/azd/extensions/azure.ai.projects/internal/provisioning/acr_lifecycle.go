// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"

	"azure.ai.projects/internal/synthesis"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/google/uuid"
)

const acrPendingConfigKey = "extensions.ai-projects.acrPending"

const (
	acrPullRole          = "7f951dda-4ed3-4680-a7ca-43fe172d538d"
	acrRepositoryReader  = "b93aa761-3e63-49ed-ac28-beffa264f7ac"
	acrRepositoryWriter  = "2a1e307c-b015-4ebd-883e-5b7698a07328"
	acrRepositoryContrib = "2efddaa5-3f1f-4df3-97df-af3f13818f4c"
)

type acrDeploymentFailure struct {
	Code    string                 `json:"code"`
	Details []acrDeploymentFailure `json:"details"`
}

func acrRoleAssignmentConflict(err error) bool {
	response, ok := errors.AsType[*azcore.ResponseError](err)
	if !ok {
		return false
	}
	if response.StatusCode == http.StatusUnauthorized || response.StatusCode == http.StatusForbidden ||
		response.StatusCode >= http.StatusInternalServerError {
		return false
	}
	if response.ErrorCode == "RoleAssignmentExists" {
		return true
	}
	if response.RawResponse == nil {
		return false
	}
	body, readErr := runtime.Payload(response.RawResponse)
	if readErr != nil {
		return false
	}
	var envelope struct {
		Error      acrDeploymentFailure `json:"error"`
		Properties struct {
			Error acrDeploymentFailure `json:"error"`
		} `json:"properties"`
	}
	if json.Unmarshal(body, &envelope) != nil {
		return false
	}
	failure := envelope.Error
	if failure.Code == "" {
		failure = envelope.Properties.Error
	}
	var conflictsOnly func(acrDeploymentFailure) bool
	conflictsOnly = func(failure acrDeploymentFailure) bool {
		if failure.Code == "RoleAssignmentExists" {
			return true
		}
		if failure.Code != "DeploymentFailed" && failure.Code != "ResourceDeploymentFailure" ||
			len(failure.Details) == 0 {
			return false
		}
		for _, detail := range failure.Details {
			if !conflictsOnly(detail) {
				return false
			}
		}
		return true
	}
	return conflictsOnly(failure)
}

type acrPendingOperation struct {
	Version            int      `json:"version"`
	RegistryResourceID string   `json:"registryResourceId"`
	ProjectResourceID  string   `json:"projectResourceId,omitempty"`
	PrincipalID        string   `json:"principalId,omitempty"`
	RoleDefinitionID   string   `json:"roleDefinitionId"`
	Token              string   `json:"token"`
	DeploymentIDs      []string `json:"deploymentIds"`
	ResourceGroupID    string   `json:"resourceGroupId"`
	ProjectName        string   `json:"projectName"`
	DeploymentSalt     string   `json:"deploymentSalt"`
}

func (o *acrPendingOperation) assignmentID() string {
	return o.RegistryResourceID + "/providers/Microsoft.Authorization/roleAssignments/" + o.Token
}

func (o *acrPendingOperation) matches(registryID, groupID, projectName, salt string) bool {
	return strings.EqualFold(registryID, o.RegistryResourceID) &&
		strings.EqualFold(groupID, o.ResourceGroupID) &&
		strings.EqualFold(projectName, o.ProjectName) && salt == o.DeploymentSalt
}

type acrAccessPlan struct {
	projectID        string
	principalID      string
	roleDefinitionID string
	pullAssigned     bool
}

type acrRoleAssignment struct {
	ID         string `json:"id"`
	Properties struct {
		PrincipalID      string `json:"principalId"`
		RoleDefinitionID string `json:"roleDefinitionId"`
		Scope            string `json:"scope"`
		Condition        string `json:"condition"`
		Description      string `json:"description"`
	} `json:"properties"`
}

type acrResource struct {
	ID       string `json:"id"`
	Identity struct {
		PrincipalID string `json:"principalId"`
	} `json:"identity"`
	Properties struct {
		RoleAssignmentMode string                         `json:"roleAssignmentMode"`
		Template           any                            `json:"template"`
		Parameters         map[string]struct{ Value any } `json:"parameters"`
	} `json:"properties"`
}

func (p *FoundryProvisioningProvider) readACRConfig(ctx context.Context, key string, out any) (bool, error) {
	if p.azdClient == nil {
		return false, nil
	}
	resp, err := p.azdClient.Environment().GetConfig(ctx, &azdext.GetConfigRequest{
		EnvName: p.envName, Path: key,
	})
	if err != nil {
		return false, fmt.Errorf("read registry lifecycle state: %w", err)
	}
	if !resp.GetFound() {
		return false, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(resp.GetValue()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(out); err != nil {
		return false, fmt.Errorf("invalid registry lifecycle state at %s: %w", key, err)
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return false, fmt.Errorf("invalid registry lifecycle state at %s: expected one object", key)
	}
	return true, nil
}

func (p *FoundryProvisioningProvider) writeACRConfig(ctx context.Context, key string, value any) error {
	if p.azdClient == nil {
		return errors.New("cannot persist registry lifecycle state without an azd environment")
	}
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = p.azdClient.Environment().SetConfig(ctx, &azdext.SetConfigRequest{
		EnvName: p.envName, Path: key, Value: data,
	})
	if err != nil {
		return fmt.Errorf("persist registry lifecycle state: %w", err)
	}
	return nil
}

func (p *FoundryProvisioningProvider) loadACRLifecycle(ctx context.Context) error {
	var raw json.RawMessage
	found, err := p.readACRConfig(ctx, synthesis.ACRBindingConfigKey, &raw)
	if err != nil {
		return err
	}
	if found {
		p.acrBinding, err = synthesis.ParseACRBinding(raw)
		if err != nil {
			return err
		}
	}
	var pending acrPendingOperation
	found, err = p.readACRConfig(ctx, acrPendingConfigKey, &pending)
	if err != nil {
		return err
	}
	if found {
		if pending.Version != 1 || uuid.Validate(pending.Token) != nil ||
			synthesis.ValidateACRSelector(pending.RegistryResourceID, "", "") != nil ||
			!strings.HasSuffix(strings.ToLower(pending.RoleDefinitionID), "/"+acrPullRole) &&
				!strings.HasSuffix(strings.ToLower(pending.RoleDefinitionID), "/"+acrRepositoryReader) {
			return errors.New("invalid pending registry access operation; refusing unsafe recovery")
		}
		group, groupErr := arm.ParseResourceID(pending.ResourceGroupID)
		if groupErr != nil || !strings.EqualFold(group.ResourceType.String(), "Microsoft.Resources/resourceGroups") ||
			pending.ProjectName == "" || strings.ContainsAny(pending.ProjectName, "/\\?#%") {
			return errors.New("invalid pending registry access deployment scope")
		}
		expected := acrAccessDeploymentIDs(pending.RegistryResourceID, pending.Token)
		if !slices.EqualFunc(expected, pending.DeploymentIDs, strings.EqualFold) {
			return errors.New("invalid registry access deployment ownership")
		}
		p.acrPending = &pending
	}
	return nil
}

func acrAccessDeploymentIDs(registryID, token string) []string {
	parts := strings.Split(registryID, "/")
	scope := strings.Join(parts[:5], "/") + "/providers/Microsoft.Resources/deployments/"
	return []string{scope + "azd-acr-access-" + token, scope + "azd-acr-" + token}
}

func (p *FoundryProvisioningProvider) acrARM(
	ctx context.Context, method, path, apiVersion string, out any,
) error {
	if p.acrRequest != nil {
		return p.acrRequest(ctx, method, path, apiVersion, out)
	}
	if err := p.ensureCredential(ctx); err != nil {
		return err
	}
	client, err := arm.NewClient("azure.ai.projects", "v1.0.0", p.credential, p.armClientOptions)
	if err != nil {
		return err
	}
	endpoint := client.Endpoint()
	address := endpoint + path
	if strings.HasPrefix(path, "https://") {
		next, err := url.Parse(path)
		if err != nil || next.User != nil || !strings.EqualFold(next.Scheme+"://"+next.Host, endpoint) {
			return errors.New("registry access pagination returned an invalid ARM endpoint")
		}
		address = path
	} else if !strings.HasPrefix(path, "/") {
		return errors.New("registry access pagination returned an invalid ARM path")
	}
	request, err := runtime.NewRequest(ctx, method, address)
	if err != nil {
		return err
	}
	query := request.Raw().URL.Query()
	if apiVersion != "" {
		query.Set("api-version", apiVersion)
	}
	request.Raw().URL.RawQuery = query.Encode()
	resp, err := client.Pipeline().Do(request)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return runtime.NewResponseError(resp)
	}
	if method == http.MethodDelete && resp.StatusCode == http.StatusAccepted {
		poller, err := runtime.NewPoller[any](resp, client.Pipeline(), nil)
		if err != nil {
			return err
		}
		_, err = poller.PollUntilDone(ctx, &runtime.PollUntilDoneOptions{Frequency: time.Second})
		return err
	}
	if out == nil || resp.StatusCode == http.StatusNoContent {
		_, err = runtime.Payload(resp)
		return err
	}
	return runtime.UnmarshalAsJSON(resp, out)
}

func acrAssignmentApplies(assignment acrRoleAssignment, registryID, principalID string) bool {
	props := assignment.Properties
	if !strings.EqualFold(props.PrincipalID, principalID) || props.Condition != "" {
		return false
	}
	scope := strings.TrimRight(strings.ToLower(props.Scope), "/")
	target := strings.ToLower(registryID)
	managementGroup := strings.HasPrefix(scope, strings.ToLower("/providers/Microsoft.Management/managementGroups/")) &&
		len(strings.Split(scope, "/")) == 5
	if scope == "" || target != scope && !strings.HasPrefix(target, scope+"/") && !managementGroup {
		return false
	}
	return true
}

func effectiveACRPull(assignment acrRoleAssignment, registryID, principalID string, abac bool) bool {
	if !acrAssignmentApplies(assignment, registryID, principalID) {
		return false
	}
	props := assignment.Properties
	role := strings.ToLower(props.RoleDefinitionID[strings.LastIndex(props.RoleDefinitionID, "/")+1:])
	if abac {
		return role == acrRepositoryReader || role == acrRepositoryWriter || role == acrRepositoryContrib
	}
	return role == acrPullRole ||
		role == "8311e382-0749-4cb8-b61a-304f252e45ec" ||
		role == "b24988ac-6180-42a0-ab88-20f7382dd24c" ||
		role == "8e3af657-a8ff-443c-a75c-2fe8c4bcb635"
}

func (p *FoundryProvisioningProvider) hasEffectiveACRPull(
	ctx context.Context, registryID, principalID string, abac bool,
) (bool, error) {
	path := registryID + "/providers/Microsoft.Authorization/roleAssignments?$filter=atScope%28%29"
	for path != "" {
		var page struct {
			Value    []acrRoleAssignment `json:"value"`
			NextLink string              `json:"nextLink"`
		}
		if err := p.acrARM(ctx, http.MethodGet, path, "2022-04-01", &page); err != nil {
			return false, fmt.Errorf("check existing registry pull access: %w", err)
		}
		for _, assignment := range page.Value {
			if effectiveACRPull(assignment, registryID, principalID, abac) {
				return true, nil
			}
			if acrAssignmentApplies(assignment, registryID, principalID) {
				allowed, err := p.customACRPull(ctx, assignment.Properties.RoleDefinitionID, abac)
				if err != nil {
					return false, err
				}
				if allowed {
					return true, nil
				}
			}
		}
		path = page.NextLink
	}
	return false, nil
}

func (p *FoundryProvisioningProvider) customACRPull(ctx context.Context, roleID string, abac bool) (bool, error) {
	name := strings.ToLower(roleID[strings.LastIndex(roleID, "/")+1:])
	if slices.Contains([]string{
		acrPullRole, acrRepositoryReader, acrRepositoryWriter, acrRepositoryContrib,
		"8311e382-0749-4cb8-b61a-304f252e45ec", "b24988ac-6180-42a0-ab88-20f7382dd24c",
		"8e3af657-a8ff-443c-a75c-2fe8c4bcb635",
	}, name) {
		return false, nil
	}
	var role struct {
		Properties struct {
			Permissions []struct {
				Actions        []string `json:"actions"`
				NotActions     []string `json:"notActions"`
				DataActions    []string `json:"dataActions"`
				NotDataActions []string `json:"notDataActions"`
			} `json:"permissions"`
		} `json:"properties"`
	}
	if err := p.acrARM(ctx, http.MethodGet, roleID, "2022-04-01", &role); err != nil {
		return false, fmt.Errorf("read custom registry role permissions: %w", err)
	}
	operations := []string{"Microsoft.ContainerRegistry/registries/pull/read"}
	if abac {
		operations = []string{
			"Microsoft.ContainerRegistry/registries/repositories/content/read",
			"Microsoft.ContainerRegistry/registries/repositories/metadata/read",
		}
	}
	for _, operation := range operations {
		allowed := false
		for _, permission := range role.Properties.Permissions {
			actions, excluded := permission.Actions, permission.NotActions
			if abac {
				actions, excluded = permission.DataActions, permission.NotDataActions
			}
			if acrActionMatches(actions, operation) && !acrActionMatches(excluded, operation) {
				allowed = true
			}
		}
		if !allowed {
			return false, nil
		}
	}
	return true, nil
}

func acrActionMatches(patterns []string, operation string) bool {
	for _, pattern := range patterns {
		pattern = "^" + strings.ReplaceAll(regexp.QuoteMeta(strings.ToLower(pattern)), `\*`, ".*") + "$"
		if matched, _ := regexp.MatchString(pattern, strings.ToLower(operation)); matched {
			return true
		}
	}
	return false
}

func parameterValue(src *templateSource, name string) any {
	if entry, ok := src.parameters[name].(map[string]any); ok {
		return entry["value"]
	}
	params, _ := src.armTemplate["parameters"].(map[string]any)
	if entry, ok := params[name].(map[string]any); ok {
		return entry["defaultValue"]
	}
	return nil
}

func (p *FoundryProvisioningProvider) validateACRTemplate(src *templateSource) error {
	if p.brownfieldEndpoint != "" {
		return nil
	}
	id, _ := parameterValue(src, "existingAcrResourceId").(string)
	include, _ := parameterValue(src, "includeAcr").(bool)
	mode := "create"
	group, _ := parameterValue(src, "resourceGroupName").(string)
	if group == "" {
		group = p.rgName
	}
	if id != "" {
		mode = "reuse-connect"
		if p.subID == "" {
			return errors.New("AZURE_SUBSCRIPTION_ID is required to validate the source-build registry subscription")
		}
		if err := synthesis.ValidateACRSelector(id, p.subID, group); err != nil {
			return err
		}
	}
	if p.acrBinding != nil {
		if include {
			if err := synthesis.ValidateACRBinding(p.acrBinding, mode, id); err != nil {
				return err
			}
		}
		projectID, err := arm.ParseResourceID(p.acrBinding.ProjectResourceID)
		if err != nil {
			return fmt.Errorf("invalid bound Foundry project resource ID: %w", err)
		}
		group, _ := parameterValue(src, "resourceGroupName").(string)
		project, _ := parameterValue(src, "foundryProjectName").(string)
		if group != "" && !strings.EqualFold(group, projectID.ResourceGroupName) ||
			project != "" && !strings.EqualFold(project, projectID.Name) ||
			!strings.EqualFold(p.subID, projectID.SubscriptionID) {
			return errors.New("registry migration is not supported: retain the bound Foundry project and deployment scope")
		}
		salt, _ := parameterValue(src, "resourceTokenSalt").(string)
		if salt != p.acrBinding.DeploymentSalt {
			return errors.New("registry migration is not supported: retain the deployed resourceTokenSalt")
		}
	}
	if include {
		p.greenfieldAcrMode = mode
		p.acrDeploymentSalt, _ = parameterValue(src, "resourceTokenSalt").(string)
	} else if p.acrBinding != nil {
		p.greenfieldAcrMode = p.acrBinding.Mode
		p.acrDeploymentSalt = p.acrBinding.DeploymentSalt
	}
	return nil
}

func (p *FoundryProvisioningProvider) prepareACRAccess(ctx context.Context, src *templateSource) error {
	if p.brownfieldEndpoint != "" {
		return nil
	}
	include, _ := parameterValue(src, "includeAcr").(bool)
	id, _ := parameterValue(src, "existingAcrResourceId").(string)
	if recorded := p.synthesisEnv["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"]; include && id == "" && recorded != "" {
		if err := p.validateACRCreateBinding(recorded); err != nil {
			return err
		}
	}
	if err := p.validateACRTemplate(src); err != nil {
		return err
	}
	if p.acrBindingRecovered {
		if err := p.writeACRConfig(ctx, synthesis.ACRBindingConfigKey, p.acrBinding); err != nil {
			return err
		}
		p.acrBindingRecovered = false
	}
	group, _ := parameterValue(src, "resourceGroupName").(string)
	if group == "" {
		group = p.rgName
	}
	projectName, _ := parameterValue(src, "foundryProjectName").(string)
	if projectName == "" {
		projectName = p.foundryName
	}
	salt, _ := parameterValue(src, "resourceTokenSalt").(string)
	groupID := "/subscriptions/" + p.subID + "/resourceGroups/" + group
	if p.acrPending != nil &&
		(!include || !p.acrPending.matches(id, groupID, projectName, salt)) {
		var actual acrRoleAssignment
		err := p.acrARM(ctx, http.MethodGet, p.acrPending.assignmentID(), "2022-04-01", &actual)
		if !isNotFound(err) {
			if err != nil {
				return fmt.Errorf("verify unfinished registry operation: %w", err)
			}
			return errors.New("an unfinished registry access operation must be recovered with the original registry; " +
				"retry provision or run azd down before selecting another registry")
		}
		if err := p.cleanupACRAccess(ctx); err != nil {
			return err
		}
		if err := p.clearACRConfig(ctx, acrPendingConfigKey); err != nil {
			return err
		}
		p.acrPending = nil
	}
	if !include || id == "" {
		return nil
	}
	plan, err := p.planACRAccess(ctx, src)
	if err != nil {
		return err
	}
	if p.acrPending != nil && plan.principalID != "" {
		pending := *p.acrPending
		pending.PrincipalID = plan.principalID
		if plan.projectID != "" {
			pending.ProjectResourceID = plan.projectID
		}
		if pending.PrincipalID != p.acrPending.PrincipalID ||
			pending.ProjectResourceID != p.acrPending.ProjectResourceID {
			if err := p.writeACRConfig(ctx, acrPendingConfigKey, &pending); err != nil {
				return err
			}
			p.acrPending = &pending
		}
	}
	if plan.pullAssigned {
		return nil
	}
	if p.acrPending == nil {
		token := uuid.NewString()
		pending := &acrPendingOperation{
			Version: 1, RegistryResourceID: id, ProjectResourceID: plan.projectID,
			PrincipalID: plan.principalID, Token: token,
			RoleDefinitionID: plan.roleDefinitionID,
			DeploymentIDs:    acrAccessDeploymentIDs(id, token),
			ResourceGroupID:  groupID,
			ProjectName:      projectName,
			DeploymentSalt:   salt,
		}
		if err := p.writeACRConfig(ctx, acrPendingConfigKey, pending); err != nil {
			return err
		}
		p.acrPending = pending
	}
	src.parameters["acrAccessOperationId"] = map[string]any{"value": p.acrPending.Token}
	return nil
}

func (p *FoundryProvisioningProvider) planACRAccess(
	ctx context.Context, src *templateSource,
) (*acrAccessPlan, error) {
	if p.brownfieldEndpoint != "" {
		return nil, nil
	}
	include, _ := parameterValue(src, "includeAcr").(bool)
	id, _ := parameterValue(src, "existingAcrResourceId").(string)
	group, _ := parameterValue(src, "resourceGroupName").(string)
	if group == "" {
		group = p.rgName
	}
	projectName, _ := parameterValue(src, "foundryProjectName").(string)
	if projectName == "" {
		projectName = p.foundryName
	}
	salt, _ := parameterValue(src, "resourceTokenSalt").(string)
	var pendingAssignment *acrRoleAssignment
	pendingMatches := false
	if p.acrPending != nil {
		groupID := "/subscriptions/" + p.subID + "/resourceGroups/" + group
		pendingMatches = include && p.acrPending.matches(id, groupID, projectName, salt)
		var err error
		pendingAssignment, err = p.readPendingACRAssignment(ctx)
		if err != nil {
			return nil, err
		}
		if !pendingMatches && pendingAssignment != nil {
			return nil, errors.New("an unfinished registry access operation must be recovered with the original registry; " +
				"retry provision or run azd down before selecting another registry")
		}
	}
	if !include || id == "" {
		return nil, nil
	}
	declared, _ := src.armTemplate["parameters"].(map[string]any)
	if declared["acrPullAssigned"] == nil || declared["acrAccessOperationId"] == nil {
		return nil, errors.New("the ejected Bicep registry access contract is outdated; " +
			"re-eject the Foundry Bicep templates before provisioning an external registry")
	}
	var registry acrResource
	if err := p.acrARM(ctx, http.MethodGet, id, "2025-11-01", &registry); err != nil {
		return nil, fmt.Errorf("read external registry permission mode: %w", err)
	}
	permissionMode := registry.Properties.RoleAssignmentMode
	abac := strings.EqualFold(permissionMode, "AbacRepositoryPermissions")
	if !abac && permissionMode != "" && !strings.EqualFold(permissionMode, "LegacyRegistryPermissions") {
		return nil, errors.New("external registry has an unsupported role assignment mode")
	}
	role := acrPullRole
	if abac {
		role = acrRepositoryReader
	}
	plan := &acrAccessPlan{
		roleDefinitionID: "/subscriptions/" + strings.Split(id, "/")[2] +
			"/providers/Microsoft.Authorization/roleDefinitions/" + role,
	}
	if p.acrBinding != nil {
		plan.projectID = p.acrBinding.ProjectResourceID
		var project acrResource
		if err := p.acrARM(ctx, http.MethodGet, plan.projectID, "2025-10-01-preview", &project); err != nil {
			return nil, fmt.Errorf("read bound Foundry project identity: %w", err)
		}
		plan.principalID = project.Identity.PrincipalID
		if !strings.EqualFold(plan.principalID, p.acrBinding.PrincipalID) {
			return nil, errors.New("bound Foundry project identity changed; refusing to reassign registry access")
		}
	} else if p.acrExistingProjectID != "" && salt == p.acrExistingProjectSalt {
		project, err := arm.ParseResourceID(p.acrExistingProjectID)
		if err != nil {
			return nil, fmt.Errorf("invalid previously deployed project ID: %w", err)
		}
		if strings.EqualFold(project.SubscriptionID, p.subID) &&
			strings.EqualFold(project.ResourceGroupName, group) && strings.EqualFold(project.Name, projectName) {
			plan.principalID, plan.projectID = p.acrExistingPrincipalID, p.acrExistingProjectID
		}
	}
	if pendingMatches {
		if pendingAssignment != nil && plan.principalID != "" &&
			!strings.EqualFold(plan.principalID, pendingAssignment.Properties.PrincipalID) {
			return nil, errors.New("pending registry assignment does not match the current Foundry project identity; " +
				"refusing to reassign registry access")
		}
		if plan.principalID == "" {
			plan.principalID, plan.projectID = p.acrPending.PrincipalID, p.acrPending.ProjectResourceID
			if pendingAssignment != nil {
				plan.principalID = pendingAssignment.Properties.PrincipalID
			}
		}
		if plan.principalID == "" {
			var err error
			plan.projectID, plan.principalID, err = p.partialProjectIdentity(ctx)
			if err != nil {
				return nil, err
			}
		}
	}
	if plan.principalID != "" {
		assigned, err := p.hasEffectiveACRPull(ctx, id, plan.principalID, abac)
		if err != nil {
			return nil, err
		}
		plan.pullAssigned = assigned
	}
	src.parameters["acrPullAssigned"] = map[string]any{"value": plan.pullAssigned}
	if pendingMatches {
		src.parameters["acrAccessOperationId"] = map[string]any{"value": p.acrPending.Token}
	}
	return plan, nil
}

func (p *FoundryProvisioningProvider) validateACRCreateBinding(registryID string) error {
	if p.acrBinding == nil {
		return errors.New("cannot establish ownership of the recorded registry from a previous deployment; " +
			"restore the deployed registry binding before provisioning or deleting the Foundry resource group")
	}
	return synthesis.ValidateACRBinding(p.acrBinding, "create", registryID)
}

func (p *FoundryProvisioningProvider) recoverACRAssignment(ctx context.Context) error {
	actual, err := p.readPendingACRAssignment(ctx)
	if err != nil || actual == nil {
		return err
	}
	pending := *p.acrPending
	pending.PrincipalID = actual.Properties.PrincipalID
	if err := p.writeACRConfig(ctx, acrPendingConfigKey, &pending); err != nil {
		return err
	}
	p.acrPending = &pending
	return nil
}

func (p *FoundryProvisioningProvider) readPendingACRAssignment(ctx context.Context) (*acrRoleAssignment, error) {
	pending := p.acrPending
	if pending == nil {
		return nil, nil
	}
	var actual acrRoleAssignment
	err := p.acrARM(ctx, http.MethodGet, pending.assignmentID(), "2022-04-01", &actual)
	if isNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("recover pending registry access: %w", err)
	}
	if !ownedACRAssignment(actual, pending.RegistryResourceID, pending.PrincipalID,
		pending.RoleDefinitionID, pending.Token) {
		return nil, errors.New("pending registry assignment does not match azd ownership; leaving it unchanged")
	}
	return &actual, nil
}

func ownedACRAssignment(actual acrRoleAssignment, registryID, principalID, roleID, token string) bool {
	return strings.EqualFold(actual.ID, registryID+"/providers/Microsoft.Authorization/roleAssignments/"+token) &&
		strings.EqualFold(actual.Properties.Scope, registryID) &&
		(principalID == "" || strings.EqualFold(actual.Properties.PrincipalID, principalID)) &&
		actual.Properties.PrincipalID != "" &&
		strings.EqualFold(actual.Properties.RoleDefinitionID, roleID) &&
		actual.Properties.Condition == "" && actual.Properties.Description == "azd-foundry:"+token
}

func (p *FoundryProvisioningProvider) persistACRBinding(
	ctx context.Context, outputs map[string]*azdext.ProvisioningOutputParameter,
) error {
	if p.brownfieldEndpoint != "" {
		return nil
	}
	if registry := outputs["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"]; p.acrBinding == nil &&
		p.greenfieldAcrMode == "none" && p.synthesisEnv["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"] != "" &&
		registry != nil && registry.Value == "" {
		delete(outputs, "AZURE_CONTAINER_REGISTRY_RESOURCE_ID")
	}
	if p.acrBinding != nil {
		registry := outputs["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"]
		if registry == nil || registry.Value == "" {
			outputs["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"] = &azdext.ProvisioningOutputParameter{
				Type: "string", Value: p.acrBinding.RegistryResourceID,
			}
			for _, key := range []string{"AZURE_CONTAINER_REGISTRY_ENDPOINT", "AZURE_AI_PROJECT_ACR_CONNECTION_NAME"} {
				if current := outputs[key]; current != nil && current.Value != "" {
					continue
				}
				value, err := p.envValue(ctx, key)
				if err != nil {
					return fmt.Errorf("preserve deployed registry output: %w", err)
				}
				outputs[key] = &azdext.ProvisioningOutputParameter{Type: "string", Value: value}
			}
		}
	}
	registry := outputs["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"]
	project := outputs["AZURE_AI_PROJECT_ID"]
	if registry == nil || registry.Value == "" || project == nil || project.Value == "" {
		return nil
	}
	var actual acrResource
	if err := p.acrARM(ctx, http.MethodGet, project.Value, "2025-10-01-preview", &actual); err != nil {
		return fmt.Errorf("read deployed project identity for registry binding: %w", err)
	}
	binding := &synthesis.ACRBinding{
		Version: 1, Mode: p.greenfieldAcrMode, RegistryResourceID: registry.Value,
		ProjectResourceID: project.Value, PrincipalID: actual.Identity.PrincipalID,
		DeploymentSalt: p.acrDeploymentSalt,
	}
	if binding.PrincipalID == "" {
		return errors.New("deployed Foundry project has no principal ID; registry binding was not saved")
	}
	if p.acrBinding != nil {
		if err := synthesis.ValidateACRBinding(p.acrBinding, binding.Mode, binding.RegistryResourceID); err != nil {
			return err
		}
		if !strings.EqualFold(binding.ProjectResourceID, p.acrBinding.ProjectResourceID) ||
			!strings.EqualFold(binding.PrincipalID, p.acrBinding.PrincipalID) {
			return errors.New("deployed project identity no longer matches the immutable registry binding")
		}
		binding.OwnedAssignments = slices.Clone(p.acrBinding.OwnedAssignments)
		if p.acrPending == nil && reflect.DeepEqual(binding, p.acrBinding) {
			return nil
		}
	}
	if p.acrPending != nil {
		p.acrPending.ProjectResourceID = project.Value
		p.acrPending.PrincipalID = binding.PrincipalID
		if err := p.recoverACRAssignment(ctx); err != nil {
			return err
		}
		var assignment acrRoleAssignment
		err := p.acrARM(ctx, http.MethodGet, p.acrPending.assignmentID(), "2022-04-01", &assignment)
		if err != nil && !isNotFound(err) {
			return err
		}
		if err == nil {
			if !ownedACRAssignment(assignment, binding.RegistryResourceID, binding.PrincipalID,
				p.acrPending.RoleDefinitionID, p.acrPending.Token) {
				return errors.New("deployed registry assignment does not match the saved access operation")
			}
			owned := synthesis.ACRAssignment{
				ID: assignment.ID, PrincipalID: binding.PrincipalID,
				RoleDefinitionID: p.acrPending.RoleDefinitionID, OwnershipToken: p.acrPending.Token,
				DeploymentIDs: p.acrPending.DeploymentIDs,
			}
			if !slices.ContainsFunc(binding.OwnedAssignments,
				func(a synthesis.ACRAssignment) bool { return strings.EqualFold(a.ID, owned.ID) }) {
				binding.OwnedAssignments = append(binding.OwnedAssignments, owned)
			}
		} else {
			operation := synthesis.ACRAssignment{
				ID: p.acrPending.assignmentID(), PrincipalID: p.acrPending.PrincipalID,
				RoleDefinitionID: p.acrPending.RoleDefinitionID, OwnershipToken: p.acrPending.Token,
				DeploymentIDs: p.acrPending.DeploymentIDs,
			}
			if err := p.cleanupACROperations(ctx, binding.RegistryResourceID, []synthesis.ACRAssignment{operation}); err != nil {
				return err
			}
		}
	}
	if err := p.writeACRConfig(ctx, synthesis.ACRBindingConfigKey, binding); err != nil {
		return err
	}
	p.acrBinding = binding
	if p.acrPending != nil {
		if err := p.clearACRConfig(ctx, acrPendingConfigKey); err != nil {
			return err
		}
		p.acrPending = nil
	}
	return p.setEnv(ctx, "AZD_FOUNDRY_ACR_MODE", binding.Mode)
}

func (p *FoundryProvisioningProvider) clearACRConfig(ctx context.Context, key string) error {
	if p.azdClient == nil {
		return errors.New("cannot clear registry lifecycle state without an azd environment")
	}
	_, err := p.azdClient.Environment().UnsetConfig(ctx, &azdext.UnsetConfigRequest{
		EnvName: p.envName, Path: key,
	})
	if err != nil {
		return fmt.Errorf("clear registry lifecycle state: %w", err)
	}
	return nil
}

func (p *FoundryProvisioningProvider) cleanupACRAccess(ctx context.Context) error {
	var assignments []synthesis.ACRAssignment
	registryID := ""
	if p.acrBinding != nil && p.acrBinding.Mode == "reuse-connect" {
		registryID = p.acrBinding.RegistryResourceID
		assignments = slices.Clone(p.acrBinding.OwnedAssignments)
	}
	if p.acrPending != nil {
		registryID = p.acrPending.RegistryResourceID
		assignments = append(assignments, synthesis.ACRAssignment{
			ID: p.acrPending.assignmentID(), PrincipalID: p.acrPending.PrincipalID,
			RoleDefinitionID: p.acrPending.RoleDefinitionID, OwnershipToken: p.acrPending.Token,
			DeploymentIDs: p.acrPending.DeploymentIDs,
		})
	}
	return p.cleanupACROperations(ctx, registryID, assignments)
}

func (p *FoundryProvisioningProvider) cleanupACROperations(
	ctx context.Context, registryID string, assignments []synthesis.ACRAssignment,
) error {
	for _, owned := range assignments {
		var actual acrRoleAssignment
		err := p.acrARM(ctx, http.MethodGet, owned.ID, "2022-04-01", &actual)
		if err != nil && !isNotFound(err) {
			return fmt.Errorf("verify owned registry grant before cleanup: %w", err)
		}
		if err == nil {
			if !ownedACRAssignment(actual, registryID, owned.PrincipalID, owned.RoleDefinitionID, owned.OwnershipToken) {
				return errors.New("registry grant no longer matches azd ownership; refusing to delete it")
			}
			err = p.acrARM(ctx, http.MethodDelete, owned.ID, "2022-04-01", nil)
			if err != nil && !isNotFound(err) {
				return fmt.Errorf("delete azd-owned registry grant: %w", err)
			}
		}
		expected := acrAccessDeploymentIDs(registryID, owned.OwnershipToken)
		if !slices.EqualFunc(expected, owned.DeploymentIDs, strings.EqualFold) {
			return errors.New("registry deployment record does not match azd ownership")
		}
		for _, id := range owned.DeploymentIDs {
			var deployment acrResource
			err := p.acrARM(ctx, http.MethodGet, id, "2022-09-01", &deployment)
			if isNotFound(err) {
				continue
			}
			if err != nil {
				return fmt.Errorf("verify registry access deployment ownership: %w", err)
			}
			params := deployment.Properties.Parameters
			outer := params["accessOperationId"].Value == owned.OwnershipToken
			inner := params["assignmentName"].Value == owned.OwnershipToken &&
				params["ownershipDescription"].Value == "azd-foundry:"+owned.OwnershipToken
			if !outer && !inner {
				return errors.New("registry access deployment no longer matches azd ownership")
			}
			err = p.acrARM(ctx, http.MethodDelete, id, "2022-09-01", nil)
			if err != nil && !isNotFound(err) {
				return fmt.Errorf("delete registry access deployment record: %w", err)
			}
		}
	}
	return nil
}

func (p *FoundryProvisioningProvider) completeDestroy(ctx context.Context) (*azdext.ProvisioningDestroyResult, error) {
	path := "/subscriptions/" + p.subID + "/providers/Microsoft.Resources/deployments/" + p.deploymentName()
	if err := p.acrARM(ctx, http.MethodDelete, path, "2022-09-01", nil); err != nil && !isNotFound(err) {
		return nil, fmt.Errorf("delete completed Foundry deployment record: %w", err)
	}
	if err := p.clearACRLifecycle(ctx); err != nil {
		return nil, err
	}
	return p.destroyResult(), nil
}

func (p *FoundryProvisioningProvider) clearACRLifecycle(ctx context.Context) error {
	if p.acrPending != nil {
		if err := p.clearACRConfig(ctx, acrPendingConfigKey); err != nil {
			return err
		}
		p.acrPending = nil
	}
	if p.acrBinding != nil {
		p.greenfieldAcrMode = p.acrBinding.Mode
		if err := p.clearACRConfig(ctx, synthesis.ACRBindingConfigKey); err != nil {
			return err
		}
		p.acrBinding = nil
	}
	return nil
}

func (p *FoundryProvisioningProvider) recoverDeployedACRBinding(ctx context.Context) error {
	if p.brownfieldEndpoint != "" || p.acrBinding != nil {
		return nil
	}
	var deployment struct {
		Properties struct {
			Outputs           map[string]struct{ Value any } `json:"outputs"`
			Parameters        map[string]struct{ Value any } `json:"parameters"`
			ProvisioningState string                         `json:"provisioningState"`
		} `json:"properties"`
	}
	path := "/subscriptions/" + p.subID + "/providers/Microsoft.Resources/deployments/" + p.deploymentName()
	if err := p.acrARM(ctx, http.MethodGet, path, "2022-09-01", &deployment); err != nil {
		if isNotFound(err) {
			return nil
		}
		return fmt.Errorf("read previous deployed registry binding: %w", err)
	}
	state := deployment.Properties.ProvisioningState
	if p.acrPending != nil && state != "" && state != "Succeeded" && state != "Failed" && state != "Canceled" {
		return errors.New("a Foundry deployment is still active; wait for it to finish before registry access recovery")
	}
	outputs := map[string]*azdext.ProvisioningOutputParameter{}
	for name, output := range deployment.Properties.Outputs {
		value, ok := output.Value.(string)
		if !ok {
			continue
		}
		outputs[strings.ToUpper(name)] = &azdext.ProvisioningOutputParameter{Type: "string", Value: value}
	}
	registry := outputs["AZURE_CONTAINER_REGISTRY_RESOURCE_ID"]
	salt := ""
	for name, parameter := range deployment.Properties.Parameters {
		if strings.EqualFold(name, "resourceTokenSalt") {
			salt, _ = parameter.Value.(string)
		}
	}
	if registry == nil || registry.Value == "" {
		project := outputs["AZURE_AI_PROJECT_ID"]
		if project != nil && project.Value != "" {
			var actual acrResource
			if err := p.acrARM(ctx, http.MethodGet, project.Value, "2025-10-01-preview", &actual); err != nil {
				return fmt.Errorf("read existing project identity before registry setup: %w", err)
			}
			p.acrExistingProjectID = project.Value
			p.acrExistingPrincipalID = actual.Identity.PrincipalID
			p.acrExistingProjectSalt = salt
			if state == "Succeeded" && p.acrPending == nil {
				return nil
			}
		}
		return p.recoverPartialACRBinding(ctx, salt)
	}
	mode := "create"
	for name, parameter := range deployment.Properties.Parameters {
		if strings.EqualFold(name, "existingAcrResourceId") {
			if id, ok := parameter.Value.(string); ok && id != "" {
				if !strings.EqualFold(id, registry.Value) {
					return errors.New("previous deployment registry output does not match its registry parameter")
				}
				mode = "reuse-connect"
			}
		}
	}
	if mode == "create" {
		id, err := arm.ParseResourceID(registry.Value)
		if err != nil || !strings.EqualFold(id.SubscriptionID, p.subID) ||
			!strings.EqualFold(id.ResourceGroupName, p.rgName) {
			return errors.New("cannot establish the previous registry ownership from deployed facts")
		}
	}
	project := outputs["AZURE_AI_PROJECT_ID"]
	if project == nil || project.Value == "" {
		return errors.New("previous deployed registry has no recorded Foundry project resource ID")
	}
	var actual acrResource
	if err := p.acrARM(ctx, http.MethodGet, project.Value, "2025-10-01-preview", &actual); err != nil {
		return fmt.Errorf("read previous deployed project identity: %w", err)
	}
	binding := &synthesis.ACRBinding{
		Version: 1, Mode: mode, RegistryResourceID: registry.Value, ProjectResourceID: project.Value,
		PrincipalID: actual.Identity.PrincipalID, DeploymentSalt: salt,
	}
	if _, err := synthesis.ParseACRBinding(binding); err != nil {
		return err
	}
	p.acrBinding = binding
	p.acrBindingRecovered = true
	return nil
}

func (p *FoundryProvisioningProvider) partialProjectIdentity(ctx context.Context) (string, string, error) {
	subscription, group, name := p.subID, p.rgName, p.foundryName
	if p.acrPending != nil {
		scope, err := arm.ParseResourceID(p.acrPending.ResourceGroupID)
		if err != nil {
			return "", "", fmt.Errorf("invalid pending registry access deployment scope: %w", err)
		}
		subscription, group, name = scope.SubscriptionID, scope.ResourceGroupName, p.acrPending.ProjectName
	}
	path := "/subscriptions/" + subscription + "/resourceGroups/" + group +
		"/providers/Microsoft.Resources/deployments/foundry-resources/operations"
	for path != "" {
		var operations struct {
			Value []struct {
				Properties struct {
					TargetResource struct {
						ID           string `json:"id"`
						ResourceType string `json:"resourceType"`
					} `json:"targetResource"`
				} `json:"properties"`
			} `json:"value"`
			NextLink string `json:"nextLink"`
		}
		err := p.acrARM(ctx, http.MethodGet, path, "2022-09-01", &operations)
		if isNotFound(err) {
			return "", "", nil
		}
		if err != nil {
			return "", "", fmt.Errorf("recover partially deployed project identity: %w", err)
		}
		for _, operation := range operations.Value {
			target := operation.Properties.TargetResource
			if !strings.EqualFold(target.ResourceType, "Microsoft.CognitiveServices/accounts/projects") {
				continue
			}
			id, err := arm.ParseResourceID(target.ID)
			if err != nil || !strings.EqualFold(id.SubscriptionID, subscription) ||
				!strings.EqualFold(id.ResourceGroupName, group) || !strings.EqualFold(id.Name, name) {
				continue
			}
			var project acrResource
			err = p.acrARM(ctx, http.MethodGet, target.ID, "2025-10-01-preview", &project)
			if isNotFound(err) {
				continue
			}
			if err != nil {
				return "", "", err
			}
			return target.ID, project.Identity.PrincipalID, nil
		}
		path = operations.NextLink
	}
	return "", "", nil
}

func (p *FoundryProvisioningProvider) recoverPartialACRBinding(ctx context.Context, salt string) error {
	projectID, principalID, err := p.partialProjectIdentity(ctx)
	if err != nil || projectID == "" {
		return err
	}
	p.acrExistingProjectID = projectID
	p.acrExistingPrincipalID = principalID
	p.acrExistingProjectSalt = salt
	var connections struct {
		Value []struct {
			Properties struct {
				Category string            `json:"category"`
				Metadata map[string]string `json:"metadata"`
			} `json:"properties"`
		} `json:"value"`
		NextLink string `json:"nextLink"`
	}
	path := projectID + "/connections"
	registryID := ""
	for path != "" {
		connections.Value = nil
		if err := p.acrARM(ctx, http.MethodGet, path, "2025-10-01-preview", &connections); err != nil {
			if isNotFound(err) {
				return nil
			}

			return fmt.Errorf("recover deployed registry connection: %w", err)
		}
		for _, connection := range connections.Value {
			if !strings.EqualFold(connection.Properties.Category, "ContainerRegistry") {
				continue
			}
			id := ""
			for key, value := range connection.Properties.Metadata {
				if strings.EqualFold(key, "ResourceId") {
					id = value
				}
			}
			if synthesis.ValidateACRSelector(id, "", "") != nil {
				return errors.New("deployed registry connection has an invalid registry resource ID")
			}
			if registryID != "" && !strings.EqualFold(registryID, id) {
				return errors.New("multiple deployed registry connections prevent safe binding recovery")
			}
			registryID = id
		}
		path = connections.NextLink
	}
	if registryID == "" {
		return nil
	}
	parts := strings.Split(registryID, "/")
	mode := "reuse-connect"
	if strings.EqualFold(parts[2], p.subID) && strings.EqualFold(parts[4], p.rgName) {
		mode = "create"
	}
	binding := &synthesis.ACRBinding{
		Version: 1, Mode: mode, RegistryResourceID: registryID,
		ProjectResourceID: projectID, PrincipalID: principalID,
		DeploymentSalt: salt,
	}
	if _, err := synthesis.ParseACRBinding(binding); err != nil {
		return err
	}
	p.acrBinding = binding
	p.acrBindingRecovered = true
	return nil
}

func (p *FoundryProvisioningProvider) destroyOrphanACRAccess(
	ctx context.Context, options *azdext.ProvisioningDestroyOptions,
) (*azdext.ProvisioningDestroyResult, error) {
	recordedScope := ""
	if p.acrBinding != nil {
		recordedScope = p.acrBinding.ProjectResourceID
	}
	if p.acrPending != nil {
		recordedScope = p.acrPending.ResourceGroupID
	}
	if recordedScope != "" {
		scope, err := arm.ParseResourceID(recordedScope)
		if err != nil {
			return nil, fmt.Errorf("invalid recorded registry deployment scope: %w", err)
		}
		oldSub, oldGroup := p.subID, p.rgName
		p.subID, p.rgName = scope.SubscriptionID, scope.ResourceGroupName
		defer func() { p.subID, p.rgName = oldSub, oldGroup }()
	}
	if !options.GetForce() {
		confirmed, err := p.confirmDestroy(ctx)
		if err != nil {
			return nil, err
		}
		if !confirmed {
			return nil, errors.New("destroy cancelled")
		}
	}
	if err := p.ensureCredential(ctx); err != nil {
		return nil, err
	}
	if err := p.cleanupACRAccess(ctx); err != nil {
		return nil, err
	}
	exists, err := p.resourceGroupExists(ctx)
	if err != nil {
		return nil, err
	}
	if exists {
		return nil, errors.New("owned external registry access was cleaned up, but the Foundry resource group " +
			"cannot be proven owned by this environment; restore its recorded ownership before retrying azd down")
	}
	return p.completeDestroy(ctx)
}
