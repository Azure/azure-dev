// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"azureaiagent/internal/exterrors"
	"azureaiagent/internal/synthesis"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
	"go.yaml.in/yaml/v3"
)

// Compile-time interface check.
var _ azdext.ProvisioningProvider = (*FoundryProvisioningProvider)(nil)

// Env keys consumed and produced by the Foundry provisioning provider.
// These align with the rest of the extension's data plane code.
const (
	envKeySubscriptionID = "AZURE_SUBSCRIPTION_ID"
	envKeyLocation       = "AZURE_LOCATION"
	envKeyResourceGroup  = "AZURE_RESOURCE_GROUP"
	envKeyProjectName    = "AZURE_AI_PROJECT_NAME"
	envKeyPrincipalID    = "AZURE_PRINCIPAL_ID"
)

// deploymentNamePrefix is prepended to the azd environment name to form
// the ARM deployment name (so re-runs of `azd provision` update the same
// deployment record).
const deploymentNamePrefix = "azd-foundry-"

// FoundryProvisioningProvider implements azdext.ProvisioningProvider for
// services whose host is one of FoundryServiceHosts. It synthesizes an
// ARM template from azure.yaml at runtime and deploys it via the ARM
// SDK; the bicep is pre-compiled into the extension binary so no bicep
// CLI is required on the user's machine.
type FoundryProvisioningProvider struct {
	azdClient *azdext.AzdClient

	// Populated by Initialize.
	synthResult *synthesis.Result
	envName     string
	subID       string
	location    string
	rgName      string
	foundryName string
	principalID string
	credential  azcore.TokenCredential
	armTemplate map[string]any
}

// NewFoundryProvisioningProvider constructs the provider with a live
// AzdClient. The host calls Initialize before any other method.
func NewFoundryProvisioningProvider(azdClient *azdext.AzdClient) azdext.ProvisioningProvider {
	return &FoundryProvisioningProvider{azdClient: azdClient}
}

// Initialize loads azure.yaml, runs the synthesizer, resolves required
// env values, and prepares an ARM credential. It rejects brownfield
// (endpoint:) and missing services with structured errors.
func (p *FoundryProvisioningProvider) Initialize(
	ctx context.Context,
	projectPath string,
	options *azdext.ProvisioningOptions,
) error {
	if options.GetProvider() != FoundryProviderName {
		// Defensive: azd routes by name, so this should never fire.
		return exterrors.Internal(
			exterrors.CodeInvalidServiceConfig,
			fmt.Sprintf("foundry provider received provider=%q (expected %q)",
				options.GetProvider(), FoundryProviderName),
		)
	}

	azureYamlPath := filepath.Join(projectPath, "azure.yaml")
	//nolint:gosec // projectPath is supplied by azd-core over gRPC and is the user's project root
	rawYAML, err := os.ReadFile(azureYamlPath)
	if err != nil {
		return exterrors.Validation(
			exterrors.CodeInvalidAzureYaml,
			fmt.Sprintf("read %s: %s", azureYamlPath, err),
			"verify azure.yaml exists at the project root",
		)
	}

	svcName, err := findFoundryService(rawYAML)
	if err != nil {
		return err
	}

	res, err := synthesis.Synthesize(synthesis.Input{
		RawAzureYAML:  rawYAML,
		ServiceName:   svcName,
		AcceptedHosts: FoundryServiceHosts,
	})
	switch {
	case errors.Is(err, synthesis.ErrEndpointBrownfield):
		return exterrors.Validation(
			exterrors.CodeBrownfieldNotSupported,
			"endpoint: is set on the foundry service; existing-project (brownfield) "+
				"provisioning is not supported yet",
			"remove endpoint: to provision a new Foundry project, or switch infra.provider to bicep",
		)
	case errors.Is(err, synthesis.ErrServiceNotFound):
		return exterrors.Validation(
			exterrors.CodeProvisioningServiceNotFound,
			fmt.Sprintf("no service in azure.yaml has host in %v", FoundryServiceHosts),
			fmt.Sprintf("add a service with `host: %s` to azure.yaml", FoundryServiceHosts[0]),
		)
	case err != nil:
		return exterrors.Validation(
			exterrors.CodeInvalidAzureYaml,
			fmt.Sprintf("synthesize foundry service %q: %s", svcName, err),
			"check the deployments/agents fields under your foundry service",
		)
	}
	p.synthResult = res

	tmplBytes, err := synthesis.ARMTemplate()
	if err != nil {
		return exterrors.Internal(
			exterrors.CodeInvalidServiceConfig,
			fmt.Sprintf("load embedded ARM template: %s", err),
		)
	}
	var tmpl map[string]any
	if err := json.Unmarshal(tmplBytes, &tmpl); err != nil {
		return exterrors.Internal(
			exterrors.CodeInvalidServiceConfig,
			fmt.Sprintf("parse embedded ARM template: %s", err),
		)
	}
	p.armTemplate = tmpl

	return p.resolveEnvAndCredential(ctx)
}

// resolveEnvAndCredential pulls the env values the provider needs and
// builds an azidentity credential bound to the right tenant.
func (p *FoundryProvisioningProvider) resolveEnvAndCredential(ctx context.Context) error {
	envClient := p.azdClient.Environment()

	currEnv, err := envClient.GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return exterrors.Dependency(
			exterrors.CodeEnvironmentNotFound,
			fmt.Sprintf("get current azd environment: %s", err),
			"run 'azd env new' to create an environment",
		)
	}
	p.envName = currEnv.Environment.Name

	get := func(key string) (string, error) {
		resp, err := envClient.GetValue(ctx, &azdext.GetEnvRequest{
			EnvName: p.envName,
			Key:     key,
		})
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(resp.Value), nil
	}

	if p.subID, err = get(envKeySubscriptionID); err != nil || p.subID == "" {
		return exterrors.Dependency(
			exterrors.CodeMissingAzureSubscription,
			fmt.Sprintf("%s is required but not set in azd environment %q", envKeySubscriptionID, p.envName),
			fmt.Sprintf("run `azd env set %s <subscription-id>`", envKeySubscriptionID),
		)
	}

	if p.location, err = get(envKeyLocation); err != nil || p.location == "" {
		return exterrors.Dependency(
			exterrors.CodeMissingAzureLocation,
			fmt.Sprintf("%s is required but not set in azd environment %q", envKeyLocation, p.envName),
			fmt.Sprintf("run `azd env set %s <region>`", envKeyLocation),
		)
	}

	if p.rgName, err = get(envKeyResourceGroup); err != nil || p.rgName == "" {
		return exterrors.Dependency(
			exterrors.CodeMissingResourceGroup,
			fmt.Sprintf("%s is required but not set in azd environment %q", envKeyResourceGroup, p.envName),
			fmt.Sprintf("run `azd env set %s <resource-group>`", envKeyResourceGroup),
		)
	}

	if p.foundryName, err = get(envKeyProjectName); err != nil || p.foundryName == "" {
		// Default to the azd environment name. Most users won't customize this.
		p.foundryName = sanitizeFoundryName(p.envName)
		log.Printf("[debug] %s not set; defaulting to %q", envKeyProjectName, p.foundryName)
	}

	// Best-effort: principalId is optional. When empty, the bicep skips
	// the developer role assignment. azd core typically populates this.
	if p.principalID, _ = get(envKeyPrincipalID); p.principalID == "" {
		log.Printf("[debug] %s not set; skipping developer role assignment", envKeyPrincipalID)
	}

	tenantResp, err := p.azdClient.Account().LookupTenant(ctx, &azdext.LookupTenantRequest{
		SubscriptionId: p.subID,
	})
	if err != nil {
		return exterrors.Auth(
			exterrors.CodeTenantLookupFailed,
			fmt.Sprintf("look up tenant for subscription %s: %s", p.subID, err),
			"run 'azd auth login' and verify access to the subscription",
		)
	}

	cred, err := azidentity.NewAzureDeveloperCLICredential(&azidentity.AzureDeveloperCLICredentialOptions{
		TenantID:                   tenantResp.TenantId,
		AdditionallyAllowedTenants: []string{"*"},
	})
	if err != nil {
		return exterrors.Auth(
			exterrors.CodeCredentialCreationFailed,
			fmt.Sprintf("create azd CLI credential: %s", err),
			"run 'azd auth login' to authenticate",
		)
	}
	p.credential = cred

	return nil
}

// EnsureEnv is a no-op past Initialize, which already verified the env
// values exist.
func (p *FoundryProvisioningProvider) EnsureEnv(ctx context.Context) error {
	return nil
}

// State returns the most recent deployment's outputs as the current
// state. If no deployment exists yet, state is empty.
func (p *FoundryProvisioningProvider) State(
	ctx context.Context,
	options *azdext.ProvisioningStateOptions,
) (*azdext.ProvisioningStateResult, error) {
	client, err := p.deploymentsClient()
	if err != nil {
		return nil, err
	}

	name := p.deploymentName()
	resp, err := client.Get(ctx, p.rgName, name, nil)
	if err != nil {
		if isNotFound(err) {
			// No deployment yet - empty state is the right answer.
			return &azdext.ProvisioningStateResult{
				State: &azdext.ProvisioningState{
					Outputs:   map[string]*azdext.ProvisioningOutputParameter{},
					Resources: []*azdext.ProvisioningResource{},
				},
			}, nil
		}
		return nil, exterrors.ServiceFromAzure(err, exterrors.OpArmDeploymentGet)
	}

	return &azdext.ProvisioningStateResult{
		State: &azdext.ProvisioningState{
			Outputs:   armOutputsToProto(deploymentOutputs(resp.Properties)),
			Resources: armResourcesToProto(deploymentResources(resp.Properties)),
		},
	}, nil
}

// Deploy runs an ARM deployment of the embedded template with the
// synthesized parameter values, streaming progress to the caller.
func (p *FoundryProvisioningProvider) Deploy(
	ctx context.Context,
	progress grpcbroker.ProgressFunc,
) (*azdext.ProvisioningDeployResult, error) {
	progress("Preparing Foundry provisioning template...")

	if err := p.ensureResourceGroup(ctx, progress); err != nil {
		return nil, err
	}

	armParams := p.armParameters()
	dep := armresources.Deployment{
		Properties: &armresources.DeploymentProperties{
			Template:   p.armTemplate,
			Parameters: armParams,
			Mode:       toPtr(armresources.DeploymentModeIncremental),
		},
		Tags: map[string]*string{
			"azd-env-name": toPtr(p.envName),
		},
	}

	client, err := p.deploymentsClient()
	if err != nil {
		return nil, err
	}

	name := p.deploymentName()
	progress(fmt.Sprintf("Starting ARM deployment %q in %s...", name, p.rgName))

	poller, err := client.BeginCreateOrUpdate(ctx, p.rgName, name, dep, nil)
	if err != nil {
		return nil, exterrors.ServiceFromAzure(err, exterrors.OpArmDeploymentCreate)
	}

	resp, err := pollWithProgress(ctx, poller, progress, "Foundry deployment in progress")
	if err != nil {
		return nil, exterrors.ServiceFromAzure(err, exterrors.OpArmDeploymentCreate)
	}

	progress("Foundry deployment complete")

	return &azdext.ProvisioningDeployResult{
		Deployment: &azdext.ProvisioningDeployment{
			Parameters: armInputsToProto(armParams),
			Outputs:    armOutputsToProto(deploymentOutputs(resp.Properties)),
		},
	}, nil
}

// Preview is not implemented for the microsoft.foundry provider.
//
// ARM what-if returns a rich change list, but azd-core's extension
// preview adapter currently drops the extension's payload (the
// `repeated changes` field is not yet on the proto), so the user would
// see an empty preview body. Returning a structured "not implemented"
// error is more honest than silently reporting "0 changes".
func (p *FoundryProvisioningProvider) Preview(
	ctx context.Context,
	progress grpcbroker.ProgressFunc,
) (*azdext.ProvisioningPreviewResult, error) {
	return nil, exterrors.Validation(
		exterrors.CodePreviewNotImplemented,
		"`azd provision --preview` is not implemented yet for the microsoft.foundry provider",
		"run `azd provision` to apply the configuration directly",
	)
}

// Destroy removes the deployment record and (if --purge is set) the
// resources it created. Without --purge we keep the resources because
// they may be shared, mirroring azd-core's bicep provider default.
func (p *FoundryProvisioningProvider) Destroy(
	ctx context.Context,
	options *azdext.ProvisioningDestroyOptions,
	progress grpcbroker.ProgressFunc,
) (*azdext.ProvisioningDestroyResult, error) {
	progress("Removing Foundry deployment record...")

	client, err := p.deploymentsClient()
	if err != nil {
		return nil, err
	}

	name := p.deploymentName()
	poller, err := client.BeginDelete(ctx, p.rgName, name, nil)
	if err != nil && !isNotFound(err) {
		return nil, exterrors.ServiceFromAzure(err, exterrors.OpArmDeploymentDelete)
	}
	if poller != nil {
		if _, err := pollWithProgress(ctx, poller, progress, "Deleting deployment"); err != nil {
			return nil, exterrors.ServiceFromAzure(err, exterrors.OpArmDeploymentDelete)
		}
	}

	// Resource-level destruction is delegated to azd core via the
	// returned env keys; we only own the deployment record here.
	return &azdext.ProvisioningDestroyResult{
		InvalidatedEnvKeys: []string{
			"AZURE_AI_PROJECT_ID",
			"AZURE_AI_ACCOUNT_NAME",
			"AZURE_AI_PROJECT_NAME",
			"AZURE_OPENAI_ENDPOINT",
			"FOUNDRY_PROJECT_ENDPOINT",
			"AZURE_CONTAINER_REGISTRY_ENDPOINT",
			"AZURE_CONTAINER_REGISTRY_RESOURCE_ID",
			"AZURE_AI_PROJECT_ACR_CONNECTION_NAME",
		},
	}, nil
}

// Parameters reports the parameter values that will be sent to ARM, so
// azd can show them in `azd provision --preview` and similar tooling.
func (p *FoundryProvisioningProvider) Parameters(
	ctx context.Context,
) ([]*azdext.ProvisioningParameter, error) {
	out := []*azdext.ProvisioningParameter{
		{Name: "location", Value: p.location, EnvVarMapping: []string{envKeyLocation}},
		{Name: "foundryProjectName", Value: p.foundryName, EnvVarMapping: []string{envKeyProjectName}},
		{Name: "principalId", Value: p.principalID, EnvVarMapping: []string{envKeyPrincipalID}},
		{Name: "includeAcr", Value: fmt.Sprintf("%v", p.synthResult.Parameters["includeAcr"])},
	}
	return out, nil
}

// PlannedOutputs declares the outputs the embedded ARM template emits
// so azd can plan dependent service env wiring.
func (p *FoundryProvisioningProvider) PlannedOutputs(
	ctx context.Context,
) ([]*azdext.ProvisioningPlannedOutput, error) {
	return []*azdext.ProvisioningPlannedOutput{
		{Name: "AZURE_AI_PROJECT_ID"},
		{Name: "AZURE_AI_ACCOUNT_NAME"},
		{Name: "AZURE_AI_PROJECT_NAME"},
		{Name: "AZURE_RESOURCE_GROUP"},
		{Name: "AZURE_OPENAI_ENDPOINT"},
		{Name: "FOUNDRY_PROJECT_ENDPOINT"},
		{Name: "AZURE_CONTAINER_REGISTRY_ENDPOINT"},
		{Name: "AZURE_CONTAINER_REGISTRY_RESOURCE_ID"},
		{Name: "AZURE_AI_PROJECT_ACR_CONNECTION_NAME"},
	}, nil
}

// --- helpers ---

// deploymentsClient builds the ARM DeploymentsClient lazily on first
// use; it relies on p.subID and p.credential set by Initialize.
func (p *FoundryProvisioningProvider) deploymentsClient() (*armresources.DeploymentsClient, error) {
	factory, err := armresources.NewClientFactory(p.subID, p.credential, nil)
	if err != nil {
		return nil, exterrors.Internal(
			exterrors.CodeAzdClientFailed,
			fmt.Sprintf("create armresources client: %s", err),
		)
	}
	return factory.NewDeploymentsClient(), nil
}

// ensureResourceGroup creates the configured RG if it doesn't exist.
// Re-runs are idempotent.
func (p *FoundryProvisioningProvider) ensureResourceGroup(
	ctx context.Context,
	progress grpcbroker.ProgressFunc,
) error {
	factory, err := armresources.NewClientFactory(p.subID, p.credential, nil)
	if err != nil {
		return exterrors.Internal(
			exterrors.CodeAzdClientFailed,
			fmt.Sprintf("create armresources client: %s", err),
		)
	}
	rgClient := factory.NewResourceGroupsClient()

	if _, err := rgClient.Get(ctx, p.rgName, nil); err == nil {
		return nil
	} else if !isNotFound(err) {
		return exterrors.ServiceFromAzure(err, exterrors.OpResourceGroupCreate)
	}

	progress(fmt.Sprintf("Creating resource group %s in %s...", p.rgName, p.location))
	_, err = rgClient.CreateOrUpdate(ctx, p.rgName, armresources.ResourceGroup{
		Location: toPtr(p.location),
		Tags:     map[string]*string{"azd-env-name": toPtr(p.envName)},
	}, nil)
	if err != nil {
		return exterrors.ServiceFromAzure(err, exterrors.OpResourceGroupCreate)
	}
	return nil
}

// deploymentName is stable per azd env so re-runs update one record.
func (p *FoundryProvisioningProvider) deploymentName() string {
	return deploymentNamePrefix + p.envName
}

// armParameters wraps the synthesizer-derived values in ARM's
// {"value": ...} envelope and merges in provider-supplied params
// (location, principal, project name).
func (p *FoundryProvisioningProvider) armParameters() map[string]any {
	out := map[string]any{
		"location":           map[string]any{"value": p.location},
		"foundryProjectName": map[string]any{"value": p.foundryName},
		"resourceTokenSalt":  map[string]any{"value": p.envName},
		"principalId":        map[string]any{"value": p.principalID},
		"tags":               map[string]any{"value": map[string]string{"azd-env-name": p.envName}},
	}
	for k, v := range p.synthResult.Parameters {
		out[k] = map[string]any{"value": v}
	}
	return out
}

// findFoundryService scans azure.yaml for a single service whose host
// matches one of FoundryServiceHosts and returns its name.
func findFoundryService(raw []byte) (string, error) {
	type svc struct {
		Host string `yaml:"host"`
	}
	type root struct {
		Services map[string]svc `yaml:"services"`
	}
	var r root
	if err := yamlUnmarshalLoose(raw, &r); err != nil {
		return "", exterrors.Validation(
			exterrors.CodeInvalidAzureYaml,
			fmt.Sprintf("parse azure.yaml: %s", err),
			"verify azure.yaml is valid YAML",
		)
	}

	var matches []string
	for name, s := range r.Services {
		if slices.Contains(FoundryServiceHosts, s.Host) {
			matches = append(matches, name)
		}
	}
	switch len(matches) {
	case 0:
		return "", exterrors.Validation(
			exterrors.CodeProvisioningServiceNotFound,
			fmt.Sprintf("no service in azure.yaml has host in %v", FoundryServiceHosts),
			fmt.Sprintf("add a service with `host: %s` to azure.yaml", FoundryServiceHosts[0]),
		)
	case 1:
		return matches[0], nil
	default:
		return "", exterrors.Validation(
			exterrors.CodeProvisioningServiceNotFound,
			fmt.Sprintf("multiple services declare a foundry host %v (%v); only one is supported",
				FoundryServiceHosts, matches),
			"keep a single foundry service per project",
		)
	}
}

// pollWithProgress streams a simple "still working" heartbeat while
// the SDK poller advances. SDK pollers don't expose granular ARM
// operation events without extra calls, so this stays coarse.
func pollWithProgress[T any](
	ctx context.Context,
	poller *runtime.Poller[T],
	progress grpcbroker.ProgressFunc,
	msg string,
) (T, error) {
	tick := time.NewTicker(5 * time.Second)
	defer tick.Stop()

	done := make(chan struct{})
	defer close(done)

	go func() {
		for {
			select {
			case <-done:
				return
			case <-tick.C:
				progress(msg)
			}
		}
	}()

	return poller.PollUntilDone(ctx, nil)
}

// deploymentOutputs returns the Outputs map from a possibly-nil
// DeploymentPropertiesExtended, defensively.
func deploymentOutputs(p *armresources.DeploymentPropertiesExtended) any {
	if p == nil {
		return nil
	}
	return p.Outputs
}

// deploymentResources returns OutputResources from a possibly-nil
// DeploymentPropertiesExtended, defensively.
func deploymentResources(p *armresources.DeploymentPropertiesExtended) []*armresources.ResourceReference {
	if p == nil {
		return nil
	}
	return p.OutputResources
}

// armOutputsToProto converts an ARM Properties.Outputs (an opaque
// any-typed map keyed by output name) into azdext outputs.
func armOutputsToProto(outputs any) map[string]*azdext.ProvisioningOutputParameter {
	out := map[string]*azdext.ProvisioningOutputParameter{}
	m, ok := outputs.(map[string]any)
	if !ok {
		return out
	}
	for k, v := range m {
		entry, ok := v.(map[string]any)
		if !ok {
			continue
		}
		typeStr, _ := entry["type"].(string)
		val := entry["value"]
		out[k] = &azdext.ProvisioningOutputParameter{
			Type:  typeStr,
			Value: fmt.Sprintf("%v", val),
		}
	}
	return out
}

// armInputsToProto converts the ARM parameters map we sent into the
// shape azdext expects for the deployment result.
func armInputsToProto(
	in map[string]any,
) map[string]*azdext.ProvisioningInputParameter {
	out := map[string]*azdext.ProvisioningInputParameter{}
	for k, v := range in {
		entry, ok := v.(map[string]any)
		if !ok {
			continue
		}
		out[k] = &azdext.ProvisioningInputParameter{
			Value: fmt.Sprintf("%v", entry["value"]),
		}
	}
	return out
}

// armResourcesToProto converts ARM output resources to the azdext shape.
func armResourcesToProto(in []*armresources.ResourceReference) []*azdext.ProvisioningResource {
	out := make([]*azdext.ProvisioningResource, 0, len(in))
	for _, r := range in {
		if r == nil || r.ID == nil {
			continue
		}
		out = append(out, &azdext.ProvisioningResource{Id: *r.ID})
	}
	return out
}

// isNotFound true if the wrapped azcore.ResponseError is a 404.
func isNotFound(err error) bool {
	respErr, ok := errors.AsType[*azcore.ResponseError](err)
	return ok && respErr.StatusCode == 404
}

// toPtr returns a pointer to its arg. Go 1.26's new() works for values
// but not when we need a typed pointer to a non-addressable expression.
func toPtr[T any](v T) *T { return &v }

// sanitizeFoundryName trims a name to the [3,32] alnum/hyphen range
// Foundry projects accept. Conservative: replaces anything else with '-'.
func sanitizeFoundryName(in string) string {
	if in == "" {
		return "foundryproject"
	}
	var b strings.Builder
	for _, r := range strings.ToLower(in) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	s := strings.Trim(b.String(), "-")
	if len(s) > 32 {
		s = s[:32]
	}
	if len(s) < 3 {
		s = s + "prj"
	}
	return s
}

// yamlUnmarshalLoose decodes YAML ignoring unknown fields. The default
// go-yaml behavior already ignores unknowns; this wrapper exists so we
// only surface real parse errors.
func yamlUnmarshalLoose(data []byte, out any) error {
	return yaml.Unmarshal(data, out)
}
