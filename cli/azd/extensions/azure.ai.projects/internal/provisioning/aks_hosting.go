// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// cspell:ignore BYOAKS kubelet kubeletidentity
// cspell:ignore federatedidentityroledefinitionid hostingprincipalid kubeletobjectid workloadprincipalid

package provisioning

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"

	"azure.ai.projects/internal/exterrors"
	"azure.ai.projects/internal/onboarding"
	"azure.ai.projects/synthesis"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
	"github.com/google/uuid"
)

type byoHostingParameters struct {
	Enabled                             bool            `json:"enabled"`
	ClusterResourceID                   json.RawMessage `json:"clusterResourceId"`
	HostingManagementIdentityResourceID json.RawMessage `json:"hostingManagementIdentityResourceId"`
	WorkloadIdentityResourceID          json.RawMessage `json:"workloadIdentityResourceId"`
}

func (p *FoundryProvisioningProvider) prepareBYO(
	ctx context.Context, source *templateSource, progress grpcbroker.ProgressFunc,
) error {
	hosting, err := byoHostingParameter(source)
	if err != nil {
		return err
	}
	registryID, _ := parameterValue(source, "existingAcrResourceId").(string)
	if hosting.Enabled && (registryID != "" ||
		p.acrBinding != nil && p.acrBinding.Mode == "reuse-connect") {
		return synthesis.ErrACRReuseAgentHosting
	}
	if !hosting.Enabled {
		return nil
	}
	var clusterID string
	if err := json.Unmarshal(hosting.ClusterResourceID, &clusterID); err != nil {
		return exterrors.Validation(exterrors.CodeInvalidParameter,
			"agentHosting.clusterResourceId must be a resource ID string for AKS binding lookups",
			"supply the complete resource ID of the effective AKS cluster")
	}
	if _, err := onboarding.ParseID(clusterID, "Microsoft.ContainerService/managedClusters"); err != nil {
		return err
	}
	hostBindings := map[string]any{
		"federatedIdentityRoleDefinitionId": nil,
		"hostingPrincipalId":                nil,
		"workloadPrincipalId":               nil,
		"byoAksExistingRoleAssignmentKeys":  nil,
	}
	if includeAcr, _ := byoParameterValue(source, "includeAcr").(bool); includeAcr {
		hostBindings["kubeletObjectId"] = nil
	}
	hostBindings = parametersDeclaredByTemplate(hostBindings, source.armTemplate)
	for name := range hostBindings {
		for supplied := range source.parameters {
			if strings.EqualFold(name, supplied) {
				delete(hostBindings, name)
				break
			}
		}
	}
	_, resolveFederationRole := hostBindings["federatedIdentityRoleDefinitionId"]
	identities := []struct {
		field, binding, id string
		value              json.RawMessage
		fact               onboarding.Identity
	}{
		{field: "hostingManagementIdentityResourceId", binding: "hostingPrincipalId",
			value: hosting.HostingManagementIdentityResourceID},
		{field: "workloadIdentityResourceId", binding: "workloadPrincipalId", value: hosting.WorkloadIdentityResourceID},
	}
	roleSubscription := p.subID
	for i := range identities {
		identity := &identities[i]
		if _, needed := hostBindings[identity.binding]; !needed &&
			!(resolveFederationRole && identity.binding == "workloadPrincipalId") {
			continue
		}
		if len(identity.value) > 0 {
			if err := json.Unmarshal(identity.value, &identity.id); err != nil {
				return exterrors.Validation(exterrors.CodeInvalidParameter,
					fmt.Sprintf("agentHosting.%s must be a resource ID string to resolve deployment bindings",
						identity.field),
					"supply a complete managed identity resource ID or provide the required deployment bindings explicitly")
			}
		}
		if identity.id == "" {
			delete(hostBindings, identity.binding)
			continue
		}
		id, err := onboarding.ParseID(identity.id, "Microsoft.ManagedIdentity/userAssignedIdentities")
		if err != nil {
			return err
		}
		if identity.binding == "workloadPrincipalId" {
			roleSubscription = id.SubscriptionID
		}
	}
	enabled, err := strconv.ParseBool(os.Getenv(synthesis.BYOAKSEnvVar))
	if err != nil || !enabled {
		return exterrors.Compatibility(
			exterrors.CodeFeatureNotEnabled,
			fmt.Sprintf("preview feature %q is disabled", synthesis.BYOAKSFeatureID),
			fmt.Sprintf("enable by setting %s=true in your shell or CI before running azd", synthesis.BYOAKSEnvVar),
		)
	}
	if err := p.ensureCredential(ctx); err != nil {
		return err
	}
	plan, err := p.identifyBYOTemplatePlan(ctx, source)
	if err != nil {
		return err
	}
	evidence := onboarding.NewAuthorizationEvidence(p.credential, p.armClientOptions)
	pipeline := onboarding.Pipeline{
		Resources: onboarding.NewResourceChecker(p.credential, p.armClientOptions),
		Access:    onboarding.NewAuthorizerWithEvidence(p.credential, p.armClientOptions, evidence),
	}
	facts, report, preflightErr := pipeline.Preflight(ctx, plan)
	if err := writeBYOPreflight(report, preflightErr, progress); err != nil {
		return err
	}
	_, needsKubelet := hostBindings["kubeletObjectId"]
	if facts.KubeletObjectID == "" && (plan.Cluster.Missing == "" || needsKubelet) {
		return fmt.Errorf("BYO AKS requires a kubelet principal object ID from preflight")
	}
	if facts.KubeletObjectID != "" {
		if _, err := uuid.Parse(facts.KubeletObjectID); err != nil {
			return fmt.Errorf("AKS kubelet principal object ID must be a GUID: %w", err)
		}
	}
	workloadScope := plan.Workload.ID
	if workloadScope == "" {
		workloadScope = plan.Workload.Parent
	}
	if resolveFederationRole && workloadScope != "" {
		if scope, err := arm.ParseResourceID(workloadScope); err == nil && scope.SubscriptionID != "" {
			roleSubscription = scope.SubscriptionID
		}
	}
	identities[0].fact, identities[1].fact = facts.HostingIdentity, facts.WorkloadIdentity
	resolvedBindings := map[string]any{"kubeletObjectId": facts.KubeletObjectID}
	if _, needed := hostBindings["byoAksExistingRoleAssignmentKeys"]; needed {
		keys, skipped, err := evidence.ExistingRoleAssignmentKeys(ctx, plan)
		if err != nil {
			return fmt.Errorf("resolve existing BYO AKS role assignments: %w", err)
		}
		if err := writeBYOPreflight(skipped, nil, progress); err != nil {
			return err
		}
		resolvedBindings["byoAksExistingRoleAssignmentKeys"] = keys
	}
	for i := range identities {
		identity := &identities[i]
		if _, needed := hostBindings[identity.binding]; !needed {
			continue
		}
		if !strings.EqualFold(identity.id, identity.fact.ID) || identity.fact.PrincipalID == "" {
			return fmt.Errorf("%s deployment binding requires matching resource ID and principal ID from preflight",
				identity.field)
		}
		if _, err := uuid.Parse(identity.fact.PrincipalID); err != nil {
			return fmt.Errorf("supplied identity principal ID must be a GUID: %w", err)
		}
		resolvedBindings[identity.binding] = identity.fact.PrincipalID
	}
	if resolveFederationRole {
		roleID, err := resolveBYOFederationRoleID(ctx, roleSubscription, evidence)
		if err != nil {
			return err
		}
		resolvedBindings["federatedIdentityRoleDefinitionId"] = roleID
	}
	for name := range hostBindings {
		hostBindings[name] = map[string]any{"value": resolvedBindings[name]}
	}
	source.parameters = mergeParameters(source.parameters, hostBindings)
	return nil
}

func writeBYOPreflight(report onboarding.Report, preflightErr error, progress grpcbroker.ProgressFunc) error {
	var output strings.Builder
	if err := report.Write(&output); err != nil {
		return errors.Join(preflightErr, err)
	}
	if output.Len() > 0 {
		progress(output.String())
	}
	return preflightErr
}

func byoARMOptions(options *arm.ClientOptions) *arm.ClientOptions {
	var result arm.ClientOptions
	if options != nil {
		result = *options
	}
	result.DisableRPRegistration = true
	return &result
}

func resolveBYOFederationRoleID(
	ctx context.Context, roleSubscription string, evidence *onboarding.AuthorizationEvidence,
) (string, error) {
	const roleName = "Managed Identity Federated Identity Credential Contributor"
	id, err := evidence.ResolveRoleByName(ctx, roleSubscription, roleName)
	if err != nil {
		return "", fmt.Errorf("resolve built-in federation role: %w", err)
	}
	roleID, err := arm.ParseResourceID(id)
	if err != nil || !strings.EqualFold(roleID.ResourceType.String(), "Microsoft.Authorization/roleDefinitions") {
		return "", fmt.Errorf("built-in federation role has an invalid ID")
	}
	if _, err := uuid.Parse(roleID.Name); err != nil {
		return "", fmt.Errorf("BYO AKS federation role requires a role definition GUID: %w", err)
	}
	return "/subscriptions/" + roleSubscription +
		"/providers/Microsoft.Authorization/roleDefinitions/" + roleID.Name, nil
}

func byoHostingParameter(source *templateSource) (byoHostingParameters, error) {
	var hosting byoHostingParameters
	var value any
	declared := false
	definitions, _ := source.armTemplate["parameters"].(map[string]any)
	for name, definition := range definitions {
		if strings.EqualFold(name, "agentHosting") {
			declared = true
			if definition, ok := definition.(map[string]any); ok {
				value = definition["defaultValue"]
			}
		}
	}
	if !declared {
		return hosting, nil
	}
	supplied := false
	for name, parameter := range source.parameters {
		if !strings.EqualFold(name, "agentHosting") {
			continue
		}
		if supplied {
			return hosting, exterrors.Validation(exterrors.CodeConflictingArguments,
				"multiple agentHosting deployment parameters differ only by case",
				"supply agentHosting only once using the template's parameter name")
		}
		supplied = true
		envelope, ok := parameter.(map[string]any)
		literal, present := envelope["value"]
		if !ok || !present || len(envelope) != 1 {
			return hosting, exterrors.Validation(exterrors.CodeInvalidParameter,
				"agentHosting deployment parameter must use a value envelope",
				"supply agentHosting as a literal object with the values needed for local AKS binding lookups")
		}
		value = literal
	}
	raw, err := json.Marshal(value)
	if err != nil {
		return hosting, fmt.Errorf("encode agentHosting deployment parameters: %w", err)
	}
	if err := json.Unmarshal(raw, &hosting); err != nil {
		return hosting, exterrors.Validation(exterrors.CodeInvalidParameter,
			fmt.Sprintf("invalid agentHosting deployment parameters: %s", err),
			"supply agentHosting as a literal object with the values needed for local AKS binding lookups")
	}
	return hosting, nil
}

func byoParameterValue(source *templateSource, key string) any {
	// Malformed explicit envelopes must not enable template-default bindings before ARM validation.
	for name, parameter := range source.parameters {
		if strings.EqualFold(name, key) {
			envelope, _ := parameter.(map[string]any)
			return envelope["value"]
		}
	}
	return parameterValue(source, key)
}
