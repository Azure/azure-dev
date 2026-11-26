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
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"

	"azure.ai.projects/internal/exterrors"
	"azure.ai.projects/internal/onboarding"
	"azure.ai.projects/synthesis"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
	"github.com/google/uuid"
)

type byoHostingParameters struct {
	Enabled                             bool            `json:"enabled"`
	ClusterResourceID                   json.RawMessage `json:"clusterResourceId"`
	HostingManagementIdentityResourceID json.RawMessage `json:"hostingManagementIdentityResourceId"`
	WorkloadIdentityResourceID          json.RawMessage `json:"workloadIdentityResourceId"`
}

type byoIdentityBinding struct {
	field, binding, id string
	value              json.RawMessage
	fact               onboarding.Identity
}

type byoLocalPreparation struct {
	standardHosting       bool
	hostBindings          map[string]any
	identities            [2]byoIdentityBinding
	roleSubscription      string
	resolveFederationRole bool
	includeAcr            bool
	explicitKubeletID     string
}

func (p *FoundryProvisioningProvider) rejectFallbackBYORegistry(source *templateSource) error {
	if p != nil && p.brownfieldEndpoint != "" {
		return nil
	}
	includeAcr, _ := byoParameterValue(source, "includeAcr").(bool)
	existingACR, _ := parameterValue(source, "existingAcrResourceId").(string)
	hasExternal := strings.TrimSpace(existingACR) != "" ||
		(p != nil && (p.acrPending != nil || (p.acrBinding != nil && p.acrBinding.Mode == "reuse-connect")))
	if !includeAcr && !hasExternal {
		return nil
	}
	return exterrors.Validation(
		exterrors.CodeInvalidParameter,
		"Foundry container registry routing with BYO AKS hosting requires agentHosting.enabled=true",
		"declare and enable the standard agentHosting parameter contract for container registry pull-principal routing",
	)
}

func (p *FoundryProvisioningProvider) prepareBYOLocal(
	source *templateSource,
) (local *byoLocalPreparation, prepareErr error) {
	p.byoHostingEnabled = false
	defer func() { prepareErr = redactBYOPreparationError(prepareErr) }()
	hosting, err := byoHostingParameter(source)
	if err != nil {
		return nil, err
	}
	if !hosting.Enabled {
		templateEnabled, planErr := p.localBYOPlan(source)
		if !templateEnabled {
			return nil, nil
		}
		if planErr != nil {
			return nil, planErr
		}
	}
	if hosting.Enabled {
		if include, _ := parameterValue(source, "includeAcr").(bool); include &&
			!templateDeclaresParameter(source, "acrPullPrincipalId") {
			return nil, errors.New("the ejected Bicep registry access contract is outdated; " +
				"re-eject the Foundry Bicep templates for kubelet-only registry access")
		}
		var clusterID string
		if err := json.Unmarshal(hosting.ClusterResourceID, &clusterID); err != nil {
			return nil, exterrors.Validation(exterrors.CodeInvalidParameter,
				"agentHosting.clusterResourceId must be a resource ID string for AKS binding lookups",
				"supply the complete resource ID of the effective AKS cluster")
		}
		if _, err := onboarding.ParseID(clusterID, "Microsoft.ContainerService/managedClusters"); err != nil {
			return nil, err
		}
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
	identities := [2]byoIdentityBinding{
		{field: "hostingManagementIdentityResourceId", binding: "hostingPrincipalId"},
		{field: "workloadIdentityResourceId", binding: "workloadPrincipalId"},
	}
	if hosting.Enabled {
		identities[0].value = hosting.HostingManagementIdentityResourceID
		identities[1].value = hosting.WorkloadIdentityResourceID
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
				return nil, exterrors.Validation(exterrors.CodeInvalidParameter,
					fmt.Sprintf("agentHosting.%s must be a resource ID string to resolve deployment bindings",
						identity.field),
					"supply a complete managed identity resource ID or provide the required deployment bindings explicitly")
			}
		}
		if identity.id == "" {
			continue
		}
		id, err := onboarding.ParseID(identity.id, "Microsoft.ManagedIdentity/userAssignedIdentities")
		if err != nil {
			return nil, err
		}
		if identity.binding == "workloadPrincipalId" {
			roleSubscription = id.SubscriptionID
		}
	}
	enabled, err := strconv.ParseBool(os.Getenv(synthesis.BYOAKSEnvVar))
	if err != nil || !enabled {
		return nil, exterrors.Compatibility(
			exterrors.CodeFeatureNotEnabled,
			fmt.Sprintf("preview feature %q is disabled", synthesis.BYOAKSFeatureID),
			fmt.Sprintf("enable by setting %s=true in your shell or CI before running azd", synthesis.BYOAKSEnvVar),
		)
	}
	if !hosting.Enabled {
		if err := p.rejectFallbackBYORegistry(source); err != nil {
			return nil, err
		}
	}
	includeAcr, _ := byoParameterValue(source, "includeAcr").(bool)
	explicitKubeletID := ""
	if includeAcr {
		for name, parameter := range source.parameters {
			if !strings.EqualFold(name, "kubeletObjectId") {
				continue
			}
			if explicitKubeletID != "" {
				return nil, exterrors.Validation(exterrors.CodeConflictingArguments,
					"multiple kubeletObjectId deployment parameters differ only by case",
					"supply kubeletObjectId only once using the template's parameter name")
			}
			envelope, ok := parameter.(map[string]any)
			principal, literal := envelope["value"].(string)
			if !ok || len(envelope) != 1 || !literal || uuid.Validate(principal) != nil {
				return nil, exterrors.Validation(exterrors.CodeInvalidParameter,
					"kubeletObjectId must use a value envelope containing a literal principal object ID GUID",
					"omit kubeletObjectId to use the selected AKS cluster's actual kubelet identity")
			}
			explicitKubeletID = principal
		}
	}
	return &byoLocalPreparation{
		standardHosting:       hosting.Enabled,
		hostBindings:          hostBindings,
		identities:            identities,
		roleSubscription:      roleSubscription,
		resolveFederationRole: resolveFederationRole,
		includeAcr:            includeAcr,
		explicitKubeletID:     explicitKubeletID,
	}, nil
}

func (p *FoundryProvisioningProvider) prepareBYO(
	ctx context.Context, source *templateSource, progress grpcbroker.ProgressFunc,
) error {
	local, err := p.prepareBYOLocal(source)
	if err != nil {
		return err
	}
	return p.prepareBYOWithLocal(ctx, source, local, progress)
}

func (p *FoundryProvisioningProvider) prepareBYOWithLocal(
	ctx context.Context, source *templateSource, local *byoLocalPreparation, progress grpcbroker.ProgressFunc,
) (prepareErr error) {
	if local == nil {
		return nil
	}
	defer func() { prepareErr = redactBYOPreparationError(prepareErr) }()
	hostBindings := local.hostBindings
	identities := local.identities
	roleSubscription := local.roleSubscription
	resolveFederationRole := local.resolveFederationRole
	includeAcr := local.includeAcr
	explicitKubeletID := local.explicitKubeletID
	if err := p.ensureCredential(ctx); err != nil {
		return err
	}
	evidence := onboarding.NewAuthorizationEvidence(p.credential, p.armClientOptions)
	plan, err := p.identifyBYOTemplatePlan(ctx, source, evidence)
	if err != nil {
		return err
	}
	// Explicit selectors still need bindings when their effective identities are also ARM deployment targets.
	for i, resource := range []*onboarding.Resource{&plan.Hosting, &plan.Workload} {
		identity := &identities[i]
		_, automatic := hostBindings[identity.binding]
		resource.PrincipalRequired = automatic && (resource.Supplied || identity.id != "")
		if !resource.PrincipalRequired {
			delete(hostBindings, identity.binding)
		}
		identity.id = resource.ID
	}
	resourceProgress := func(detail string) {
		message := "Checking AKS and supporting resources"
		if detail != "" {
			message += ": " + detail
		}
		progress(message + "...")
	}
	pipeline := onboarding.Pipeline{
		Resources: onboarding.NewResourceChecker(p.credential, p.armClientOptions, evidence, resourceProgress),
		Access:    evidence,
		BeforeResourceCheck: func() {
			resourceProgress("")
		},
		BeforeAccessCheck: func() {
			progress("Checking required permissions...")
		},
	}
	facts, _, err := pipeline.Preflight(ctx, plan)
	if err != nil {
		return err
	}
	progress("Resolving deployment identity and role bindings...")
	_, needsKubelet := hostBindings["kubeletObjectId"]
	if facts.KubeletObjectID == "" && (plan.Cluster.Missing == "" || needsKubelet || includeAcr) {
		return fmt.Errorf("BYO AKS requires a kubelet principal object ID from preflight")
	}
	if facts.KubeletObjectID != "" {
		if _, err := uuid.Parse(facts.KubeletObjectID); err != nil {
			return fmt.Errorf("AKS kubelet principal object ID must be a GUID: %w", err)
		}
	}
	if explicitKubeletID != "" && !strings.EqualFold(explicitKubeletID, facts.KubeletObjectID) {
		return exterrors.Validation(exterrors.CodeConflictingArguments,
			fmt.Sprintf("kubeletObjectId %q does not match the actual kubelet principal object ID %q for AKS %q",
				explicitKubeletID, facts.KubeletObjectID, plan.Cluster.ID),
			"remove kubeletObjectId to resolve it automatically, or set it to the selected AKS cluster's actual kubelet ID")
	}
	p.byoHostingEnabled = true
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
	federationRoleID, _ := byoParameterValue(source, "federatedIdentityRoleDefinitionId").(string)
	if resolveFederationRole {
		var err error
		federationRoleID, err = resolveBYOFederationRoleID(ctx, roleSubscription, evidence)
		if err != nil {
			return err
		}
		resolvedBindings["federatedIdentityRoleDefinitionId"] = federationRoleID
	}
	if _, needed := hostBindings["byoAksExistingRoleAssignmentKeys"]; needed {
		grantFacts := facts
		for name, principal := range map[string]*string{
			"hostingPrincipalId":  &grantFacts.HostingIdentity.PrincipalID,
			"workloadPrincipalId": &grantFacts.WorkloadIdentity.PrincipalID,
		} {
			if _, automatic := hostBindings[name]; automatic {
				continue
			}
			if value, supplied := byoParameterValue(source, name).(string); supplied {
				*principal = value
			}
		}
		keys, _, err := evidence.ExistingRoleAssignmentKeys(ctx, plan, grantFacts, federationRoleID)
		if err != nil {
			if _, blocked := errors.AsType[*onboarding.BlockedError](err); blocked {
				return err
			}
			return fmt.Errorf("resolve existing BYO AKS role assignments: %w", err)
		}
		resolvedBindings["byoAksExistingRoleAssignmentKeys"] = keys
	}
	for name := range hostBindings {
		hostBindings[name] = map[string]any{"value": resolvedBindings[name]}
	}
	source.parameters = mergeParameters(source.parameters, hostBindings)
	return nil
}

// Terminal controls delimit URLs in OSC8 hyperlinks and must not be consumed by URL parsing.
var byoErrorURLPattern = regexp.MustCompile(`(?i)https?://[^\s<>"\x00-\x1f\x7f-\x9f]+`)

func redactBYOErrorMessage(message string) string {
	return byoErrorURLPattern.ReplaceAllStringFunc(message, func(raw string) string {
		endpoint, err := url.Parse(raw)
		if err != nil {
			return "[redacted URL]"
		}
		endpoint.User, endpoint.RawQuery, endpoint.Fragment = nil, "", ""
		endpoint.ForceQuery = false
		return endpoint.String()
	})
}

func redactBYOPreparationError(err error) error {
	if err == nil {
		return nil
	}
	message := redactBYOErrorMessage(err.Error())
	wire := azdext.WrapError(err)
	if message == err.Error() && wire.GetMessage() == message {
		return err
	}
	// The SDK prefers structured messages over outer Error() text; redact a classified copy, not the shared cause.
	wire.Message = message
	var classified error
	if wire.GetOrigin() != azdext.ErrorOrigin_ERROR_ORIGIN_UNSPECIFIED {
		classified = azdext.UnwrapError(wire)
	}
	if original, ok := errors.AsType[*azdext.ToolError](err); ok &&
		wire.GetOrigin() == azdext.ErrorOrigin_ERROR_ORIGIN_TOOL {
		value := new(*original)
		value.Message = message
		classified = value
	}
	return &byoPreparationError{cause: err, classified: classified, message: message}
}

type byoPreparationError struct {
	cause, classified error
	message           string
}

func (e *byoPreparationError) Error() string { return e.message }

func (e *byoPreparationError) Unwrap() []error {
	if e.classified == nil {
		return []error{e.cause}
	}
	return []error{e.classified, e.cause}
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
