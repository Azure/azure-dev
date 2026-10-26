// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices/v2"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
)

// FoundrySpec binds the standard deployment to exact owned targets, not inferred names during deletion.
type FoundrySpec struct {
	AccountID      string          `json:"accountId"`
	ProjectID      string          `json:"projectId"`
	RegistryID     string          `json:"registryId"`
	RegistryABAC   bool            `json:"registryABAC"`
	Location       string          `json:"location"`
	ModelNames     []string        `json:"modelNames"`
	TemplateHash   string          `json:"templateHash,omitempty"`
	TemplateDigest string          `json:"templateDigest,omitempty"`
	Deployment     *DeploymentData `json:"deployment,omitempty"`
}

// FoundryAttempt is persisted before submitting one uniquely named ARM deployment.
type FoundryAttempt struct {
	ID                string `json:"id"`
	CorrelationID     string `json:"correlationId,omitempty"`
	Terminal          bool   `json:"terminal"`
	TemplateHash      string `json:"templateHash,omitempty"`
	TemplateDigest    string `json:"templateDigest,omitempty"`
	ARMTemplateHash   string `json:"armTemplateHash,omitempty"`
	OwnershipObserved bool   `json:"ownershipObserved,omitempty"`
	ProvisioningState string `json:"provisioningState,omitempty"`
}

// FoundryState preserves control-plane ownership and teardown progress across interruptions.
type FoundryState struct {
	Spec                 FoundrySpec          `json:"spec"`
	Attempts             []FoundryAttempt     `json:"attempts"`
	ProjectPrincipalID   string               `json:"projectPrincipalId,omitempty"`
	ProjectEndpoint      string               `json:"projectEndpoint,omitempty"`
	ModelsDeleted        map[string]bool      `json:"modelsDeleted,omitempty"`
	ConnectionDeleted    bool                 `json:"connectionDeleted,omitempty"`
	ProjectDeleteStarted bool                 `json:"projectDeleteStarted,omitempty"`
	ProjectDeleted       bool                 `json:"projectDeleted,omitempty"`
	Network              *FoundryNetworkState `json:"network,omitempty"`
	AccountDeleted       bool                 `json:"accountDeleted"`
	Purged               bool                 `json:"purged"`
}

// Lifecycle journals one ARM graph and observes exact ownership for cleanup.
type Lifecycle interface {
	Check(context.Context, Plan, FoundrySpec) (Report, error)
	Start(context.Context, Plan, FoundrySpec, string) error
	Observe(context.Context, Plan) error
	Cleanup(context.Context, Plan, bool, func(string)) error
}

type ownedLifecycle struct {
	credential azcore.TokenCredential
	options    *arm.ClientOptions
	journal    Journal
}

// NewLifecycle uses read-only ARM observation before exact journaled cleanup.
func NewLifecycle(credential azcore.TokenCredential, options *arm.ClientOptions, journal Journal) Lifecycle {
	return &ownedLifecycle{credential: credential, options: onboardingARMOptions(options), journal: journal}
}

// PlannedFoundryAccount returns the actual BYO account target bound into the standard template.
func PlannedFoundryAccount(plan Plan) Resource {
	name := "ais-" + plan.Owner
	parent := "/subscriptions/" + plan.SubscriptionID + "/resourceGroups/" + plan.ResourceGroup
	return Resource{ID: parent + "/providers/Microsoft.CognitiveServices/accounts/" + name, Name: name, Parent: parent}
}

// CleanupPlan resolves only persisted target scope, independent of the current YAML or environment defaults.
func CleanupPlan(state PreparationState) (Plan, error) {
	if state.Version != preparationJournalVersion || state.Owner == "" ||
		state.SubscriptionID == "" || state.ResourceGroup == "" || state.Location == "" {
		return Plan{}, fmt.Errorf("legacy/unknown ownership journal lacks an authoritative cleanup target")
	}
	plan := Plan{
		Owner: state.Owner, SubscriptionID: state.SubscriptionID, ResourceGroup: state.ResourceGroup, Location: state.Location,
	}
	if _, err := ParseID("/subscriptions/"+plan.SubscriptionID+"/resourceGroups/"+plan.ResourceGroup,
		"Microsoft.Resources/resourceGroups"); err != nil {
		return Plan{}, fmt.Errorf("persisted cleanup target is invalid: %w", err)
	}
	if err := validateCleanupLedger(plan, state); err != nil {
		return Plan{}, err
	}
	return plan, nil
}

func (l *ownedLifecycle) load(ctx context.Context, plan Plan) (PreparationState, error) {
	if l.journal == nil || plan.Owner == "" {
		return PreparationState{}, fmt.Errorf("owned lifecycle requires a durable journal and environment owner")
	}
	state, err := l.journal.Load(ctx)
	if err != nil {
		return state, fmt.Errorf("read owned lifecycle journal: %w", err)
	}
	if state.Version > preparationJournalVersion || (state.Version < preparationJournalVersion && state.Owner != "") {
		return state, fmt.Errorf("legacy/unknown journal cannot prove whether Foundry was deployed; resolve its lifecycle first")
	}
	if state.Owner != "" && state.Owner != plan.Owner {
		return state, fmt.Errorf("owned lifecycle journal belongs to another environment")
	}
	if state.Owner != "" && (!strings.EqualFold(state.SubscriptionID, plan.SubscriptionID) ||
		!strings.EqualFold(state.ResourceGroup, plan.ResourceGroup) ||
		!strings.EqualFold(state.Location, plan.Location) || state.Location == "") {
		return state, fmt.Errorf("journaled target scope/location is missing or differs from the environment")
	}
	if state.Owner == "" && (state.Foundry != nil || len(state.Resources) > 0 || len(state.Assignments) > 0 ||
		len(state.ResourceProgress) > 0) {
		return state, fmt.Errorf("owned lifecycle journal has entries without an owner")
	}
	return state, nil
}

func validateFoundrySpec(plan Plan, spec FoundrySpec) error {
	account, err := ParseID(spec.AccountID, "Microsoft.CognitiveServices/accounts")
	if err != nil || !strings.EqualFold(spec.AccountID, PlannedFoundryAccount(plan).ID) {
		return fmt.Errorf("Foundry account does not match the internally planned target")
	}
	project, err := ParseID(spec.ProjectID, "Microsoft.CognitiveServices/accounts/projects")
	if err != nil || !strings.EqualFold(project.Parent.String(), account.String()) || spec.Location == "" ||
		!strings.EqualFold(spec.Location, plan.Location) {
		return fmt.Errorf("Foundry project/location does not match the exact account target")
	}
	if _, err := ParseID(spec.RegistryID, "Microsoft.ContainerRegistry/registries"); err != nil {
		return fmt.Errorf("Foundry lifecycle registry is invalid: %w", err)
	}
	for _, name := range spec.ModelNames {
		if name == "" || strings.ContainsAny(name, "/\\?#%") {
			return fmt.Errorf("Foundry model deployment name is invalid")
		}
	}
	return nil
}

func sameFoundrySpec(a, b FoundrySpec) bool {
	same := strings.EqualFold(a.AccountID, b.AccountID) && strings.EqualFold(a.ProjectID, b.ProjectID) &&
		strings.EqualFold(a.Location, b.Location) && strings.EqualFold(a.RegistryID, b.RegistryID) &&
		a.RegistryABAC == b.RegistryABAC
	if !same || a.Deployment == nil || b.Deployment == nil {
		return same && a.Deployment == nil && b.Deployment == nil
	}
	x, y := a.Deployment, b.Deployment
	return strings.EqualFold(x.ClusterID, y.ClusterID) && strings.EqualFold(x.StorageID, y.StorageID) &&
		strings.EqualFold(x.Hosting.ID, y.Hosting.ID) && strings.EqualFold(x.Workload.ID, y.Workload.ID) &&
		strings.EqualFold(x.KubeletObjectID, y.KubeletObjectID) && strings.EqualFold(x.TenantID, y.TenantID) &&
		strings.EqualFold(x.FederatedIdentityRoleDefinitionID, y.FederatedIdentityRoleDefinitionID) &&
		x.Hosting.Supplied == y.Hosting.Supplied && x.Workload.Supplied == y.Workload.Supplied &&
		x.HostingIdentity == y.HostingIdentity && x.WorkloadIdentity == y.WorkloadIdentity
}

func (l *ownedLifecycle) Check(ctx context.Context, plan Plan, spec FoundrySpec) (Report, error) {
	check := resourceCheck("I03", spec.AccountID, "exact Foundry creation/reuse and deployment ownership")
	check.Status = Fail
	fail := func(err error) (Report, error) {
		check.Observed = RedactDiagnostic(err.Error())
		check.Missing = "unambiguous persisted lifecycle ownership; retain the journal and resolve the reported state"
		return Report{check}, err
	}
	if err := validateFoundrySpec(plan, spec); err != nil {
		return fail(err)
	}
	state, err := l.load(ctx, plan)
	if err != nil {
		return fail(err)
	}
	if spec.Deployment != nil {
		if err := validateDeploymentData(plan, spec, state); err != nil {
			return fail(err)
		}
	}
	if err := validateHistoricalNetwork(state); err != nil {
		return fail(err)
	}
	if !state.CleanupComplete && state.Owner != "" && state.Foundry == nil &&
		(len(state.Resources) > 0 || len(state.Assignments) > 0) {
		return fail(fmt.Errorf("legacy dependency ownership remains; retain the journal and complete azd down before deployment"))
	}
	if state.CleanupStarted && !state.CleanupComplete {
		return fail(fmt.Errorf("owned cleanup is incomplete; resume azd down before provisioning"))
	}
	if !state.CleanupComplete && state.Registry != nil &&
		(!strings.EqualFold(state.Registry.ID, spec.RegistryID) ||
			state.Registry.Verified && state.Registry.ABAC != spec.RegistryABAC) {
		return fail(fmt.Errorf("registry differs from the recorded deployment binding"))
	}
	if state.Foundry != nil {
		if !state.CleanupComplete && state.Foundry.Spec.Deployment == nil && spec.Deployment != nil {
			return fail(fmt.Errorf("legacy deployment ownership remains; complete exact owned cleanup before the new ARM graph"))
		}
		if !state.CleanupComplete && !sameFoundrySpec(state.Foundry.Spec, spec) {
			return fail(fmt.Errorf("Foundry selections differ from the active ownership journal"))
		}
		if err := l.verifyAttempts(ctx, plan, state.Foundry); err != nil {
			return fail(err)
		}
	}
	if err := l.checkDeploymentResources(ctx, plan, state); err != nil {
		return fail(err)
	}
	account, err := ParseID(spec.AccountID, "Microsoft.CognitiveServices/accounts")
	if err != nil {
		return fail(err)
	}
	client, err := armcognitiveservices.NewAccountsClient(account.SubscriptionID, l.credential, l.options)
	if err != nil {
		return fail(err)
	}
	found, err := client.Get(ctx, account.ResourceGroupName, account.Name, nil)
	if err == nil {
		if state.Foundry == nil || state.Foundry.AccountDeleted ||
			!strings.EqualFold(authString(found.ID), spec.AccountID) ||
			authString(found.Tags[preparationOwnerTag]) != plan.Owner ||
			!strings.EqualFold(authString(found.Location), spec.Location) {
			return fail(fmt.Errorf("Foundry account is not the exact journaled owner-tagged account; refusing adoption"))
		}
	} else if !responseNotFound(err) {
		return fail(fmt.Errorf("read Foundry creation target: %w", err))
	} else if state.Foundry != nil && state.Foundry.AccountDeleted && !state.Foundry.Purged {
		deleted, err := armcognitiveservices.NewDeletedAccountsClient(account.SubscriptionID, l.credential, l.options)
		if err != nil {
			return fail(err)
		}
		if _, err := deleted.Get(ctx, spec.Location, account.ResourceGroupName, account.Name, nil); err == nil {
			return fail(fmt.Errorf("Foundry account remains soft-deleted; explicitly use azd down --purge or await retention"))
		} else if !responseNotFound(err) {
			return fail(fmt.Errorf("verify soft-deleted Foundry name reservation: %w", err))
		}
	}
	check.Status, check.Observed = Pass, "exact account is absent or has matching durable ownership; no active ARM attempt"
	return Report{check}, nil
}

func (l *ownedLifecycle) Start(ctx context.Context, plan Plan, spec FoundrySpec, deploymentID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := validateFoundrySpec(plan, spec); err != nil {
		return err
	}
	if spec.Deployment != nil && spec.TemplateHash == "" {
		return fmt.Errorf("deployment intent requires the generated template version before submission")
	}
	if spec.Deployment != nil && spec.TemplateDigest == "" {
		return fmt.Errorf("deployment intent requires the actual template content digest before submission")
	}
	_, err := subscriptionDeploymentID(deploymentID, plan.SubscriptionID)
	if err != nil {
		return fmt.Errorf("Foundry attempt requires an exact subscription deployment ID")
	}
	state, err := l.load(ctx, plan)
	if err != nil {
		return err
	}
	if state.Owner == "" && spec.Deployment == nil {
		return fmt.Errorf("a new deployment requires internally planned graph bindings")
	}
	if state.CleanupStarted && !state.CleanupComplete {
		return fmt.Errorf("Foundry attempt requires no pending cleanup")
	}
	if state.Owner != "" && state.Foundry == nil && !state.CleanupComplete &&
		(len(state.Resources) > 0 || len(state.Assignments) > 0) {
		return fmt.Errorf("legacy dependency ownership remains; complete owned cleanup before starting a new deployment")
	}
	if !state.CleanupComplete && state.Foundry != nil &&
		state.Foundry.Spec.Deployment == nil && spec.Deployment != nil {
		return fmt.Errorf("legacy deployment ownership remains; complete exact owned cleanup before the new ARM graph")
	}
	if err := validateHistoricalNetwork(state); err != nil {
		return err
	}
	if !state.CleanupComplete && state.Registry != nil &&
		(!strings.EqualFold(state.Registry.ID, spec.RegistryID) ||
			state.Registry.Verified && state.Registry.ABAC != spec.RegistryABAC) {
		return fmt.Errorf("Foundry attempt registry does not match the recorded binding")
	}
	if spec.Deployment != nil {
		if err := l.checkDeploymentResources(ctx, plan, state); err != nil {
			return err
		}
		if err := validateDeploymentData(plan, spec, state); err != nil {
			return err
		}
	}
	if state.CleanupComplete {
		state.Resources = map[string]OwnedResource{}
		state.Assignments = map[string]OwnedAssignment{}
		state.Network = nil
	}
	state.Version = preparationJournalVersion
	state.Owner, state.SubscriptionID = plan.Owner, plan.SubscriptionID
	state.ResourceGroup, state.Location = plan.ResourceGroup, plan.Location
	if err := validateProgressSelection(plan, state); err != nil {
		return err
	}
	if state.ResourceProgress == nil {
		state.ResourceProgress = selectedProgress(plan)
	}
	if state.Resources == nil {
		state.Resources = map[string]OwnedResource{}
	}
	if state.Assignments == nil {
		state.Assignments = map[string]OwnedAssignment{}
	}
	if spec.Deployment != nil {
		for step, resource := range deploymentResources(plan) {
			if resource.Supplied {
				continue
			}
			if previous, exists := state.Resources[step]; exists {
				if !strings.EqualFold(previous.ID, resource.ID) || previous.Deleted {
					return fmt.Errorf("%s: planned resource differs from active ownership", step)
				}
			} else {
				state.Resources[step] = OwnedResource{ID: resource.ID}
			}
		}
	}
	state.Registry = &RegistryBinding{ID: spec.RegistryID, ABAC: spec.RegistryABAC}
	if state.Foundry == nil || state.CleanupComplete {
		state.Foundry = &FoundryState{Spec: spec}
	} else if state.Foundry.AccountDeleted {
		state.Foundry = &FoundryState{Spec: spec, Attempts: slices.Clone(state.Foundry.Attempts)}
	}
	if !sameFoundrySpec(state.Foundry.Spec, spec) {
		return fmt.Errorf("Foundry attempt does not match the journaled selections")
	}
	if spec.Deployment != nil && len(state.Foundry.Attempts) > 0 {
		for _, attempt := range state.Foundry.Attempts {
			if strings.EqualFold(attempt.ID, deploymentID) {
				return fmt.Errorf("previous ARM deployment ID was reused")
			}
		}
		if err := l.verifyAttempts(ctx, plan, state.Foundry); err != nil {
			return err
		}
		if err := l.observeDeployment(ctx, plan, &state); err != nil {
			return err
		}
	}
	for _, name := range spec.ModelNames {
		if !slices.ContainsFunc(state.Foundry.Spec.ModelNames, func(old string) bool { return strings.EqualFold(old, name) }) {
			state.Foundry.Spec.ModelNames = append(state.Foundry.Spec.ModelNames, name)
		}
	}
	for _, attempt := range state.Foundry.Attempts {
		if !attempt.Terminal || strings.EqualFold(attempt.ID, deploymentID) {
			return fmt.Errorf("previous ARM attempt is unresolved or its deployment ID was reused")
		}
	}
	state.Foundry.Spec.TemplateHash = spec.TemplateHash
	state.Foundry.Spec.TemplateDigest = spec.TemplateDigest
	state.Foundry.Spec.Deployment = spec.Deployment
	state.Foundry.Attempts = append(state.Foundry.Attempts, FoundryAttempt{
		ID: deploymentID, TemplateHash: spec.TemplateHash, TemplateDigest: spec.TemplateDigest,
	})
	state.Foundry.AccountDeleted, state.Foundry.Purged = false, false
	state.CleanupStarted, state.CleanupComplete = false, false
	return l.journal.Save(ctx, state)
}

func (l *ownedLifecycle) verifyAttempts(ctx context.Context, plan Plan, foundry *FoundryState) error {
	if len(foundry.Attempts) == 0 {
		return fmt.Errorf("Foundry lifecycle has no exact ARM attempt evidence")
	}
	seen := map[string]bool{}
	for index := range foundry.Attempts {
		attempt := &foundry.Attempts[index]
		id, err := subscriptionDeploymentID(attempt.ID, plan.SubscriptionID)
		if err != nil || seen[strings.ToLower(attempt.ID)] {
			return fmt.Errorf("invalid or duplicate recorded Foundry deployment ID")
		}
		if attempt.TemplateHash == "" {
			return fmt.Errorf("recorded deployment's actual template binding is unverified")
		}
		seen[strings.ToLower(attempt.ID)] = true
		client, err := armresources.NewDeploymentsClient(id.SubscriptionID, l.credential, l.options)
		if err != nil {
			return err
		}
		observed, err := client.GetAtSubscriptionScope(ctx, id.Name, nil)
		if responseNotFound(err) && attempt.Terminal && attempt.CorrelationID != "" {
			continue
		}
		if err != nil {
			return fmt.Errorf("recorded deployment is unknown; refusing dependency deletion or another deployment: %w", err)
		}
		properties := observed.Properties
		if !strings.EqualFold(authString(observed.ID), attempt.ID) ||
			authString(observed.Tags[preparationOwnerTag]) != plan.Owner || properties == nil {
			return fmt.Errorf("recorded deployment ownership changed or is unverified")
		}
		actualHash := authString(properties.TemplateHash)
		if actualHash == "" || attempt.ARMTemplateHash != "" && actualHash != attempt.ARMTemplateHash {
			return fmt.Errorf("recorded deployment's actual template binding is unverified or changed")
		}
		if err := verifyAttemptTemplate(ctx, client, id.Name, *attempt); err != nil {
			return err
		}
		var parameters map[string]struct {
			Value string `json:"value"`
		}
		raw, err := json.Marshal(properties.Parameters)
		if err != nil {
			return err
		}
		// Only decode the ownership bindings; ordinary parameters include non-string values.
		var values map[string]json.RawMessage
		if err := json.Unmarshal(raw, &values); err != nil {
			return fmt.Errorf("deployment ownership parameters are unavailable: %w", err)
		}
		for _, key := range []string{"resourceGroupName", "location", "foundryProjectName"} {
			var value struct {
				Value string `json:"value"`
			}
			if err := json.Unmarshal(values[key], &value); err != nil {
				return fmt.Errorf("deployment ownership parameter %s is unavailable: %w", key, err)
			}
			if parameters == nil {
				parameters = map[string]struct {
					Value string `json:"value"`
				}{}
			}
			parameters[key] = value
		}
		var owner, accountName string
		if foundry.Spec.Deployment != nil {
			var binding struct {
				Value struct {
					Owner                 string `json:"owner"`
					AccountName           string `json:"accountName"`
					ClusterID             string `json:"clusterResourceId"`
					StorageID             string `json:"storageAccountResourceId"`
					RegistryID            string `json:"registryResourceId"`
					HostingName           string `json:"hostingIdentityName"`
					WorkloadName          string `json:"workloadIdentityName"`
					HostingID             string `json:"hostingIdentityResourceId"`
					WorkloadID            string `json:"workloadIdentityResourceId"`
					HostingSupplied       *bool  `json:"hostingIdentitySupplied"`
					WorkloadSupplied      *bool  `json:"workloadIdentitySupplied"`
					HostingPrincipal      string `json:"hostingPrincipalId"`
					WorkloadPrincipal     string `json:"workloadPrincipalId"`
					CreateClusterGrants   *bool  `json:"createClusterGrants"`
					CreateFederationGrant *bool  `json:"createFederationGrant"`
					CreateStorageGrant    *bool  `json:"createStorageGrant"`
				} `json:"value"`
			}
			if err := json.Unmarshal(values["byoOnboarding"], &binding); err != nil {
				return fmt.Errorf("deployment graph ownership binding is unavailable: %w", err)
			}
			data := foundry.Spec.Deployment
			for _, identity := range []struct {
				id, expected, principal, actualPrincipal string
				supplied                                 *bool
				expectedSupplied                         bool
			}{
				{binding.Value.HostingID, data.Hosting.ID, binding.Value.HostingPrincipal,
					data.HostingIdentity.PrincipalID, binding.Value.HostingSupplied, data.Hosting.Supplied},
				{binding.Value.WorkloadID, data.Workload.ID, binding.Value.WorkloadPrincipal,
					data.WorkloadIdentity.PrincipalID, binding.Value.WorkloadSupplied, data.Workload.Supplied},
			} {
				if identity.expectedSupplied && (identity.id == "" || identity.supplied == nil ||
					!strings.EqualFold(identity.principal, identity.actualPrincipal)) ||
					identity.id != "" && !strings.EqualFold(identity.id, identity.expected) ||
					identity.supplied != nil && *identity.supplied != identity.expectedSupplied {
					return fmt.Errorf("recorded deployment supplied-identity binding changed or is unavailable")
				}
			}
			for _, operation := range []struct {
				selected *bool
				expected bool
			}{
				{binding.Value.CreateClusterGrants, !data.Hosting.Supplied},
				{binding.Value.CreateFederationGrant, !data.Hosting.Supplied || !data.Workload.Supplied},
				{binding.Value.CreateStorageGrant, !data.Workload.Supplied || !data.StorageCustomerSupplied},
			} {
				if operation.selected != nil && *operation.selected != operation.expected ||
					operation.selected == nil && (data.Hosting.Supplied || data.Workload.Supplied) {
					return fmt.Errorf("recorded deployment grant operation binding changed or is unavailable")
				}
			}
			if !strings.EqualFold(binding.Value.ClusterID, data.ClusterID) ||
				!strings.EqualFold(binding.Value.StorageID, data.StorageID) ||
				!strings.EqualFold(binding.Value.RegistryID, data.RegistryID) ||
				binding.Value.HostingName != data.Hosting.Name || binding.Value.WorkloadName != data.Workload.Name {
				return fmt.Errorf("recorded deployment dependency graph changed")
			}
			owner, accountName = binding.Value.Owner, binding.Value.AccountName
		} else {
			for _, key := range []string{"byoOwner", "byoAccountName"} {
				var value struct {
					Value string `json:"value"`
				}
				if err := json.Unmarshal(values[key], &value); err != nil {
					return fmt.Errorf("legacy deployment ownership parameter %s unavailable: %w", key, err)
				}
				if key == "byoOwner" {
					owner = value.Value
				} else {
					accountName = value.Value
				}
			}
		}
		account, err := ParseID(foundry.Spec.AccountID, "Microsoft.CognitiveServices/accounts")
		if err != nil || owner != plan.Owner ||
			accountName != account.Name ||
			!strings.EqualFold(parameters["resourceGroupName"].Value, plan.ResourceGroup) ||
			!strings.EqualFold(parameters["location"].Value, plan.Location) {
			return fmt.Errorf("recorded deployment target/ownership parameters changed")
		}
		project, err := ParseID(foundry.Spec.ProjectID, "Microsoft.CognitiveServices/accounts/projects")
		if err != nil || !strings.EqualFold(parameters["foundryProjectName"].Value, project.Name) {
			return fmt.Errorf("recorded deployment project target changed")
		}
		correlation := authString(properties.CorrelationID)
		if correlation == "" || (attempt.CorrelationID != "" && attempt.CorrelationID != correlation) {
			return fmt.Errorf("recorded deployment correlation is missing or changed")
		}
		if properties.ProvisioningState == nil {
			return fmt.Errorf("recorded deployment state is unknown")
		}
		switch string(*properties.ProvisioningState) {
		case "Succeeded", "Failed", "Canceled":
			attempt.Terminal, attempt.CorrelationID = true, correlation
			attempt.ProvisioningState = string(*properties.ProvisioningState)
			attempt.ARMTemplateHash = actualHash
		default:
			return fmt.Errorf("recorded deployment is still active; wait for it to terminate before proceeding")
		}
	}
	return nil
}

func subscriptionDeploymentID(value, subscription string) (*arm.ResourceID, error) {
	if !validResourceIDSyntax(value) {
		return nil, fmt.Errorf("invalid subscription deployment ID syntax")
	}
	id, err := arm.ParseResourceID(value)
	if err != nil || id.ResourceGroupName != "" || !strings.EqualFold(id.SubscriptionID, subscription) ||
		!strings.EqualFold(id.ResourceType.String(), "Microsoft.Resources/deployments") || id.Name == "" ||
		!strings.EqualFold(value, "/subscriptions/"+subscription+"/providers/Microsoft.Resources/deployments/"+id.Name) {
		return nil, fmt.Errorf("invalid subscription deployment target")
	}
	return id, nil
}

func (l *ownedLifecycle) Observe(ctx context.Context, plan Plan) error {
	state, err := l.load(ctx, plan)
	if err != nil {
		return err
	}
	if state.Foundry == nil {
		return fmt.Errorf("Foundry lifecycle intent is missing")
	}
	if err := l.verifyAttempts(ctx, plan, state.Foundry); err != nil {
		return err
	}
	if err := l.observeDeployment(ctx, plan, &state); err != nil {
		return err
	}
	if err := l.journal.Save(ctx, state); err != nil {
		return err
	}
	if len(state.Foundry.Attempts) > 0 &&
		state.Foundry.Attempts[len(state.Foundry.Attempts)-1].ProvisioningState == "Succeeded" {
		for _, key := range []string{"storageAccount", "containerRegistry", "hostingIdentity", "workloadIdentity"} {
			if !state.ResourceProgress[key].PreparationComplete {
				return fmt.Errorf(
					"ARM deployment succeeded but %s preparation evidence is incomplete; retain the journal and retry", key)
			}
		}
	}
	return nil
}
