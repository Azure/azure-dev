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
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices/v2"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.yaml.in/yaml/v3"
)

// Compile-time interface check.
var _ azdext.ProvisioningProvider = (*FoundryProvisioningProvider)(nil)

// Env keys consumed and produced by the Foundry provisioning provider.
const (
	envKeySubscriptionID = "AZURE_SUBSCRIPTION_ID"
	envKeyLocation       = "AZURE_LOCATION"
	envKeyResourceGroup  = "AZURE_RESOURCE_GROUP"
	envKeyProjectName    = "AZURE_AI_PROJECT_NAME"
	envKeyPrincipalID    = "AZURE_PRINCIPAL_ID"
)

// deploymentNamePrefix is prepended to the azd environment name to form
// the ARM deployment name so re-runs update the same deployment record.
const deploymentNamePrefix = "azd-foundry-"

// FoundryProvisioningProvider implements azdext.ProvisioningProvider for
// services whose host is one of FoundryServiceHosts. By default it deploys
// the extension's pre-compiled ARM template (no bicep CLI required). When
// ./infra/main.bicep or ./infra/main.bicepparam exists on disk (e.g. after
// `azd ai agent init --infra`), it compiles that Bicep at runtime instead
// and the user owns the parameter contract. See ondisk_template.go.
type FoundryProvisioningProvider struct {
	azdClient *azdext.AzdClient

	// Populated by Initialize.
	projectPath  string
	synthResult  *synthesis.Result // nil when onDiskSource != nil
	envName      string
	subID        string
	location     string
	rgName       string
	foundryName  string
	principalID  string
	credential   azcore.TokenCredential
	armTemplate  map[string]any  // embedded ARM JSON; nil when onDiskSource is set
	onDiskSource *templateSource // non-nil when ./infra/main.{bicep,bicepparam} exists

	// Lazily constructed on first compile. nil until needed.
	bicepCliInstance bicepCompiler
}

// NewFoundryProvisioningProvider constructs the provider with a live
// AzdClient. The host calls Initialize before any other method.
func NewFoundryProvisioningProvider(azdClient *azdext.AzdClient) azdext.ProvisioningProvider {
	return &FoundryProvisioningProvider{azdClient: azdClient}
}

// Initialize loads azure.yaml, decides between the embedded ARM template
// and the on-disk Bicep path, and resolves required env values. It rejects
// brownfield (endpoint:) and missing services with structured errors.
//
// Initialize is cheap by contract: it does no network I/O and builds no
// credential. Tenant lookup and credential construction happen lazily in
// [FoundryProvisioningProvider.ensureCredential]; the bicep CLI is built
// only when an on-disk template actually needs compiling. azd-core may
// call Initialize on providers it never deploys with, so keeping it cheap
// lets pure metadata calls (Parameters, PlannedOutputs) succeed without auth.
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
	p.projectPath = projectPath

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

	// Detect on-disk Bicep before synthesizing. Stat-only; no compile here.
	if p.onDiskTemplatePresent() {
		log.Printf("[debug] foundry provider: on-disk Bicep detected under %s; "+
			"skipping synthesizer", filepath.Join(projectPath, onDiskInfraDir))
		// endpoint: (brownfield) is rejected even on the on-disk path.
		if err := rejectBrownfield(rawYAML, svcName); err != nil {
			return err
		}
		return p.resolveEnv(ctx)
	}

	res, err := synthesis.Synthesize(synthesis.Input{
		RawAzureYAML:  rawYAML,
		ServiceName:   svcName,
		AcceptedHosts: FoundryServiceHosts,
		Env:           p.networkEnvMap(ctx),
	})
	switch {
	case errors.Is(err, synthesis.ErrEndpointBrownfield):
		// network: has no effect in brownfield mode; warn if both are present.
		warnNetworkIgnoredInBrownfield(rawYAML, svcName)
		return exterrors.Validation(
			exterrors.CodeBrownfieldNotSupported,
			"endpoint: is set on the foundry service; existing-project (brownfield) "+
				"provisioning is not supported yet",
			"remove endpoint: to provision a new Foundry project, or switch infra.provider to bicep",
		)
	case errors.Is(err, synthesis.ErrServiceNotFound):
		return exterrors.Dependency(
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
	log.Printf("[debug] foundry provider: network mode = %q", res.NetworkMode)

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

	return p.resolveEnv(ctx)
}

// networkEnvMap returns a best-effort name -> value map of the azd environment
// for ${VAR} substitution in network fields during synthesis. It does not
// require resolveEnv to have run; on any failure it returns nil and the
// synthesizer falls back to the process environment.
func (p *FoundryProvisioningProvider) networkEnvMap(ctx context.Context) map[string]string {
	if p.azdClient == nil {
		return nil
	}
	envClient := p.azdClient.Environment()
	if envClient == nil {
		return nil
	}
	curr, err := envClient.GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil || curr.GetEnvironment() == nil {
		return nil
	}
	resp, err := envClient.GetValues(ctx, &azdext.GetEnvironmentRequest{Name: curr.GetEnvironment().GetName()})
	if err != nil {
		log.Printf("[debug] foundry provider: GetValues failed (%s); network ${VAR} uses process env only", err)
		return nil
	}
	out := make(map[string]string, len(resp.GetKeyValues()))
	for _, kv := range resp.GetKeyValues() {
		if kv != nil {
			out[kv.Key] = kv.Value
		}
	}
	return out
}

// warnNetworkIgnoredInBrownfield logs a warning when a service declares both
// endpoint: (brownfield) and network:. The account's network posture is fixed
// by whoever created it, so the network: block has no effect.
func warnNetworkIgnoredInBrownfield(rawYAML []byte, svcName string) {
	type svc struct {
		Endpoint string    `yaml:"endpoint,omitempty"`
		Network  yaml.Node `yaml:"network,omitempty"`
	}
	type root struct {
		Services map[string]svc `yaml:"services"`
	}
	var r root
	if err := yaml.Unmarshal(rawYAML, &r); err != nil {
		return
	}
	s := r.Services[svcName]
	if s.Endpoint != "" && !s.Network.IsZero() {
		log.Printf("[warn] foundry provider: service %q sets both endpoint: and network:; "+
			"network: is ignored in brownfield mode (the account's network posture is fixed)", svcName)
	}
}

// or infra/main.bicep exists under p.projectPath. Stat-only.
func (p *FoundryProvisioningProvider) onDiskTemplatePresent() bool {
	infraDir := filepath.Join(p.projectPath, onDiskInfraDir)
	return fileExistsAt(filepath.Join(infraDir, onDiskBicepParamFile)) ||
		fileExistsAt(filepath.Join(infraDir, onDiskBicepFile))
}

// rejectBrownfield refuses an on-disk service that sets endpoint:, matching
// the synthesizer's ErrEndpointBrownfield branch (which the on-disk path skips).
func rejectBrownfield(rawYAML []byte, svcName string) error {
	type svc struct {
		Endpoint string `yaml:"endpoint,omitempty"`
	}
	type root struct {
		Services map[string]svc `yaml:"services"`
	}
	var r root
	if err := yaml.Unmarshal(rawYAML, &r); err != nil {
		// Malformed yaml is surfaced upstream; don't mask the parser error.
		return nil
	}
	if r.Services[svcName].Endpoint == "" {
		return nil
	}
	return exterrors.Validation(
		exterrors.CodeBrownfieldNotSupported,
		"endpoint: is set on the foundry service; existing-project (brownfield) "+
			"provisioning is not supported yet",
		"remove endpoint: to provision a new Foundry project, or switch infra.provider to bicep",
	)
}

// resolveEnv pulls the env values the provider needs from azd-core. It does
// no Azure work; that is deferred to ensureCredential.
func (p *FoundryProvisioningProvider) resolveEnv(ctx context.Context) error {
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
		// Default to the azd environment name.
		p.foundryName = sanitizeFoundryName(p.envName)
		log.Printf("[debug] %s not set; defaulting to %q", envKeyProjectName, p.foundryName)
	}

	// principalId is optional; when empty the bicep skips the developer role assignment.
	if p.principalID, _ = get(envKeyPrincipalID); p.principalID == "" {
		log.Printf("[debug] %s not set; skipping developer role assignment", envKeyPrincipalID)
	}

	return nil
}

// ensureCredential lazily looks up the subscription's tenant and builds the
// azd-CLI credential, caching it for subsequent calls.
func (p *FoundryProvisioningProvider) ensureCredential(ctx context.Context) error {
	if p.credential != nil {
		return nil
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

// EnsureEnv is a no-op; Initialize already verified the env values exist.
func (p *FoundryProvisioningProvider) EnsureEnv(ctx context.Context) error {
	return nil
}

// State returns the most recent deployment's outputs as the current state,
// or empty state when no deployment exists yet.
func (p *FoundryProvisioningProvider) State(
	ctx context.Context,
	options *azdext.ProvisioningStateOptions,
) (*azdext.ProvisioningStateResult, error) {
	client, err := p.deploymentsClient(ctx)
	if err != nil {
		return nil, err
	}

	name := p.deploymentName()
	resp, err := client.GetAtSubscriptionScope(ctx, name, nil)
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

// Deploy runs an ARM deployment of the resolved template (embedded ARM JSON
// or the user's on-disk Bicep) with the appropriate parameters, streaming
// progress to the caller.
func (p *FoundryProvisioningProvider) Deploy(
	ctx context.Context,
	progress grpcbroker.ProgressFunc,
) (*azdext.ProvisioningDeployResult, error) {
	progress("Preparing Foundry provisioning template...")

	// provision.network_mode telemetry: none | byo | managed. Lets us measure
	// secured-agent adoption and the BYO-vs-managed split.
	networkMode := synthesis.NetworkModeNone
	if p.synthResult != nil && p.synthResult.NetworkMode != "" {
		networkMode = p.synthResult.NetworkMode
	}
	trace.SpanFromContext(ctx).SetAttributes(
		attribute.String("provision.network_mode", networkMode))

	src, err := p.resolveTemplate(ctx, progress)
	if err != nil {
		return nil, err
	}

	dep := armresources.Deployment{
		Location: new(p.location),
		Properties: &armresources.DeploymentProperties{
			Template:   src.armTemplate,
			Parameters: src.parameters,
			Mode:       toPtr(armresources.DeploymentModeIncremental),
		},
		Tags: map[string]*string{
			"azd-env-name":                  new(p.envName),
			"azd-provision-template-source": new(src.mode.String()),
		},
	}

	client, err := p.deploymentsClient(ctx)
	if err != nil {
		return nil, err
	}

	name := p.deploymentName()
	progress(fmt.Sprintf("Starting ARM deployment %q...", name))

	poller, err := client.BeginCreateOrUpdateAtSubscriptionScope(ctx, name, dep, nil)
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
			Parameters: armInputsToProto(src.parameters),
			Outputs:    armOutputsToProto(deploymentOutputs(resp.Properties)),
		},
	}, nil
}

// resolveTemplate returns the on-disk Bicep source if present, else the
// embedded ARM JSON. Lazy: compiles on-disk Bicep on first call and caches
// the result on the provider so re-runs skip the bicep CLI.
//
// On the on-disk path the user's parameters are layered OVER host-derived
// parameters (location, principalId, etc.), so azd-provided values still
// flow through for keys the user's file doesn't declare. The user wins on
// keys present in both.
func (p *FoundryProvisioningProvider) resolveTemplate(
	ctx context.Context,
	progress grpcbroker.ProgressFunc,
) (*templateSource, error) {
	if p.onDiskSource == nil && p.onDiskTemplatePresent() {
		progress("Compiling on-disk Bicep templates...")
		src, err := loadOnDiskTemplate(ctx, p.projectPath, p.bicepCli(), p.envValues(ctx))
		if err != nil {
			return nil, err
		}
		if src == nil {
			// Raced with the user deleting the file mid-call; fall back to embedded.
			log.Printf("[debug] on-disk template disappeared between presence check and load; " +
				"falling back to embedded template")
		} else {
			p.onDiskSource = src
		}
	}

	if p.onDiskSource != nil {
		log.Printf("[debug] foundry provider: using on-disk template at %s", p.onDiskSource.sourcePath)
		merged := mergeParameters(p.onDiskSource.parameters, p.armParameters())
		return &templateSource{
			mode:        p.onDiskSource.mode,
			armTemplate: p.onDiskSource.armTemplate,
			parameters:  merged,
			sourcePath:  p.onDiskSource.sourcePath,
		}, nil
	}

	return &templateSource{
		mode:        templateModeEmbedded,
		armTemplate: p.armTemplate,
		parameters:  p.armParameters(),
	}, nil
}

// bicepCli lazily constructs a *bicep.Cli using azd-core's download-on-demand
// wrapper. The first call on a machine without bicep triggers a download under
// a spinner; subsequent calls reuse the cached binary.
func (p *FoundryProvisioningProvider) bicepCli() bicepCompiler {
	if p.bicepCliInstance != nil {
		return p.bicepCliInstance
	}
	console := input.NewConsole(
		false, // noPrompt
		true,  // isTerminal
		input.Writers{Output: os.Stdout},
		input.ConsoleHandles{
			Stderr: os.Stderr,
			Stdin:  os.Stdin,
			Stdout: os.Stdout,
		},
		nil, // formatter
		nil, // externalPromptCfg
	)
	p.bicepCliInstance = bicep.NewCli(console, exec.NewCommandRunner(nil))
	return p.bicepCliInstance
}

// envValues returns the resolved name -> value map of the azd environment,
// used for ${VAR} substitution in main.parameters.json and as the env passed
// to `bicep build-params`. Initialize-resolved values are surfaced under their
// canonical names so a user's ${AZURE_LOCATION} reference works even before
// their azd env file persists them.
func (p *FoundryProvisioningProvider) envValues(ctx context.Context) map[string]string {
	out := map[string]string{
		envKeySubscriptionID: p.subID,
		envKeyLocation:       p.location,
		envKeyResourceGroup:  p.rgName,
		envKeyProjectName:    p.foundryName,
		envKeyPrincipalID:    p.principalID,
	}
	// Also surface the broader azd env. Best-effort: fall back to the
	// canonical values above if the env service is unavailable.
	if p.azdClient == nil {
		return out
	}
	envClient := p.azdClient.Environment()
	if envClient == nil {
		return out
	}
	resp, err := envClient.GetValues(ctx, &azdext.GetEnvironmentRequest{Name: p.envName})
	if err != nil {
		log.Printf("[debug] foundry provider: GetValues failed (%s); ${VAR} substitution will use canonical keys only", err)
		return out
	}
	for _, kv := range resp.GetKeyValues() {
		if kv == nil {
			continue
		}
		// Don't overwrite the canonical values we just set.
		if _, taken := out[kv.Key]; taken {
			continue
		}
		out[kv.Key] = kv.Value
	}
	return out
}

// Preview runs an ARM what-if against the resolved template (same template
// and parameter selection as Deploy, but read-only). It returns a structured
// diff in ProvisioningPreviewResult.Summary AND emits that summary via the
// progress callback, because azd-core's extension preview adapter currently
// drops the Summary field. The progress emission becomes redundant once the
// core proto exposes the change set.
//
// What-if runs at subscription scope so it works without creating the resource
// group first; the group appears in the change set as a Create.
//
// Inline what-if failures (HTTP 200 with Properties.Error populated) are
// surfaced as CodeArmWhatIfFailed; without that check ARM preflight failures
// would silently look like "0 changes".
func (p *FoundryProvisioningProvider) Preview(
	ctx context.Context,
	progress grpcbroker.ProgressFunc,
) (*azdext.ProvisioningPreviewResult, error) {
	progress("Computing deployment plan...")

	src, err := p.resolveTemplate(ctx, progress)
	if err != nil {
		return nil, err
	}

	client, err := p.deploymentsClient(ctx)
	if err != nil {
		return nil, err
	}

	whatIf := armresources.DeploymentWhatIf{
		Location: new(p.location),
		Properties: &armresources.DeploymentWhatIfProperties{
			Template:   src.armTemplate,
			Parameters: src.parameters,
			Mode:       toPtr(armresources.DeploymentModeIncremental),
		},
	}

	poller, err := client.BeginWhatIfAtSubscriptionScope(ctx, p.deploymentName(), whatIf, nil)
	if err != nil {
		return nil, exterrors.ServiceFromAzure(err, exterrors.OpArmDeploymentWhatIf)
	}

	resp, err := pollWithProgress(ctx, poller, progress, "What-if analysis in progress")
	if err != nil {
		return nil, exterrors.ServiceFromAzure(err, exterrors.OpArmDeploymentWhatIf)
	}

	// Inline what-if failure: ARM returns HTTP 200 but populates Properties.Error.
	if err := whatIfFailure(resp.WhatIfOperationResult); err != nil {
		return nil, err
	}

	// Summary is kept for diagnostics/telemetry; the core preview UX renders
	// the structured Changes (colored per change type).
	return &azdext.ProvisioningPreviewResult{
		Preview: &azdext.ProvisioningDeploymentPreview{
			Summary: summarizeWhatIf(resp.WhatIfOperationResult),
			Changes: whatIfChanges(resp.WhatIfOperationResult),
		},
	}, nil
}

// Destroy tears down the Foundry deployment.
//
//   - Force == false (default): refuse with a structured error. This provider
//     does not prompt, so deletion must be an explicit --force choice.
//   - Force == true: delete every model deployment under the resource group's
//     Cognitive Services accounts, then delete the resource group (Foundry
//     account, project, and any ACR). Deployments must go first: Azure refuses
//     to delete an account that still has them, which would roll the RG delete
//     back. Idempotent: a missing RG is a no-op success.
//   - Purge == true: in addition to deleting the RG, purge each soft-deleted
//     Cognitive Services account that was inside it. Without --purge the
//     account stays soft-deleted and Azure refuses to re-create one with the
//     same name until the soft-delete retention expires (~48h), causing the
//     next `azd provision` to fail with FlagMustBeSetForRestore. Mirrors the
//     built-in bicep provider's purge flow: enumerate live accounts BEFORE
//     RG delete (capturing name+location), delete the RG (which soft-deletes
//     them), then purge each via DeletedAccountsClient.
//
// Hard-fails on purge errors: if the user asked to purge and we can't, the
// silent alternative is to leave a leftover that reproduces this same bug
// on the next provision. If the RG is already gone at Destroy time the
// enumeration step is skipped (idempotent re-run).
func (p *FoundryProvisioningProvider) Destroy(
	ctx context.Context,
	options *azdext.ProvisioningDestroyOptions,
	progress grpcbroker.ProgressFunc,
) (*azdext.ProvisioningDestroyResult, error) {
	if !options.GetForce() {
		return nil, exterrors.Validation(
			exterrors.CodeDestroyRequiresForce,
			fmt.Sprintf("microsoft.foundry destroy will delete resource group %q "+
				"and all resources inside it; this provider does not prompt for "+
				"confirmation, so --force is required", p.rgName),
			"re-run with `azd down --force` (add `--purge` to also purge "+
				"soft-deleted Cognitive Services accounts)",
		)
	}

	if err := p.ensureCredential(ctx); err != nil {
		return nil, err
	}
	factory, err := armresources.NewClientFactory(p.subID, p.credential, nil)
	if err != nil {
		return nil, exterrors.Internal(
			exterrors.CodeAzdClientFailed,
			fmt.Sprintf("create armresources client: %s", err),
		)
	}
	rgClient := factory.NewResourceGroupsClient()

	// Enumerate the live Cognitive Services accounts in the RG up front. We
	// need them for two reasons: (1) Azure refuses to delete an account that
	// still has model deployments, so those must be removed before the RG
	// delete; (2) once the RG is gone only DeletedAccountsClient can see the
	// accounts, so --purge must capture name+location now. Returns nil if the
	// RG is already gone (the BeginDelete below handles the idempotent no-op).
	accounts, err := p.listAccountsInRG(ctx, progress)
	if err != nil {
		return nil, err
	}

	// Delete model deployments before the RG delete; otherwise the account
	// delete fails with CannotDeleteAccountWithDeployments and rolls the
	// whole RG deletion back.
	if err := p.deleteModelDeployments(ctx, progress, accounts); err != nil {
		return nil, err
	}

	var toPurge []purgeableAccount
	if options.GetPurge() {
		toPurge = collectPurgeableAccounts(accounts)
	}

	progress(fmt.Sprintf("Deleting resource group %s...", p.rgName))
	poller, err := rgClient.BeginDelete(ctx, p.rgName, nil)
	if err != nil {
		if isNotFound(err) {
			// Already gone; treat as success so re-runs are idempotent. If
			// --purge was requested but the RG never existed there's nothing
			// to purge (we never enumerated anything). A soft-deleted
			// account from a prior incomplete cleanup is out of scope --
			// the user can purge it manually via `az cognitiveservices
			// account purge`.
			return invalidatedEnvKeysResult(), nil
		}
		return nil, exterrors.ServiceFromAzure(err, exterrors.OpResourceGroupDelete)
	}
	if _, err := pollWithProgress(ctx, poller, progress,
		fmt.Sprintf("Deleting resource group %s (this can take several minutes)", p.rgName),
	); err != nil {
		return nil, exterrors.ServiceFromAzure(err, exterrors.OpResourceGroupDelete)
	}

	// After the RG is gone the accounts are in the soft-deleted state.
	// Purge each one so the next `azd provision` can re-use the same name.
	if len(toPurge) > 0 {
		if err := p.purgeCognitiveAccounts(ctx, progress, toPurge); err != nil {
			return nil, err
		}
	}

	return invalidatedEnvKeysResult(), nil
}

// purgeableAccount captures the minimum state required to purge a
// soft-deleted Cognitive Services account: its name and the location it
// was created in (the soft-delete record is keyed by location, not by
// resource group alone).
type purgeableAccount struct {
	name     string
	location string
}

// collectPurgeableAccounts filters live Cognitive Services account models
// down to the {name, location} pairs needed for a post-RG-delete purge.
// Entries with a nil Name or Location are skipped (defensive against
// partial SDK responses); duplicates are not de-duplicated since the SDK
// list-by-RG call doesn't return them.
//
// Pure helper for unit testing -- the live pager call lives in Destroy.
func collectPurgeableAccounts(accounts []*armcognitiveservices.Account) []purgeableAccount {
	out := make([]purgeableAccount, 0, len(accounts))
	for _, a := range accounts {
		if a == nil || a.Name == nil || a.Location == nil {
			continue
		}
		out = append(out, purgeableAccount{name: *a.Name, location: *a.Location})
	}
	return out
}

// listAccountsInRG enumerates the live Cognitive Services accounts in the
// configured resource group via the SDK pager. Returns nil with no error if
// the RG doesn't exist (the not-found path is handled by the caller's later
// BeginDelete short-circuit). The result feeds both deployment deletion and,
// when --purge is set, the post-delete purge.
func (p *FoundryProvisioningProvider) listAccountsInRG(
	ctx context.Context,
	progress grpcbroker.ProgressFunc,
) ([]*armcognitiveservices.Account, error) {
	accountsClient, err := armcognitiveservices.NewAccountsClient(p.subID, p.credential, nil)
	if err != nil {
		return nil, exterrors.Internal(
			exterrors.CodeAzdClientFailed,
			fmt.Sprintf("create armcognitiveservices client: %s", err),
		)
	}

	progress(fmt.Sprintf("Listing Cognitive Services accounts in %s...", p.rgName))

	var accounts []*armcognitiveservices.Account
	pager := accountsClient.NewListByResourceGroupPager(p.rgName, nil)
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			if isNotFound(err) {
				// RG missing: nothing to enumerate; the BeginDelete path
				// will handle the idempotent no-op success.
				return nil, nil
			}
			return nil, exterrors.ServiceFromAzure(err, exterrors.OpCognitiveAccountList)
		}
		accounts = append(accounts, page.Value...)
	}

	return accounts, nil
}

// deleteModelDeployments removes every model deployment under each account so
// the subsequent resource-group delete can delete the accounts. Azure rejects
// deleting a Cognitive Services account that still has deployments
// (CannotDeleteAccountWithDeployments), which otherwise rolls back the entire
// RG deletion. No-op when there are no accounts/deployments; hard-fails on the
// first error so a stuck deployment surfaces instead of a confusing RG-delete
// rollback later.
func (p *FoundryProvisioningProvider) deleteModelDeployments(
	ctx context.Context,
	progress grpcbroker.ProgressFunc,
	accounts []*armcognitiveservices.Account,
) error {
	if len(accounts) == 0 {
		return nil
	}

	deploymentsClient, err := armcognitiveservices.NewDeploymentsClient(p.subID, p.credential, nil)
	if err != nil {
		return exterrors.Internal(
			exterrors.CodeAzdClientFailed,
			fmt.Sprintf("create armcognitiveservices deployments client: %s", err),
		)
	}

	for _, account := range accounts {
		if account == nil || account.Name == nil {
			continue
		}
		accountName := *account.Name

		pager := deploymentsClient.NewListPager(p.rgName, accountName, nil)
		for pager.More() {
			page, err := pager.NextPage(ctx)
			if err != nil {
				return exterrors.ServiceFromAzure(err, exterrors.OpCognitiveDeploymentList)
			}
			for _, deployment := range page.Value {
				if deployment == nil || deployment.Name == nil {
					continue
				}
				name := *deployment.Name
				progress(fmt.Sprintf("Deleting model deployment %s on %s...", name, accountName))
				poller, err := deploymentsClient.BeginDelete(ctx, p.rgName, accountName, name, nil)
				if err != nil {
					return exterrors.ServiceFromAzure(err, exterrors.OpCognitiveDeploymentDelete)
				}
				if _, err := pollWithProgress(ctx, poller, progress,
					fmt.Sprintf("Deleting model deployment %s (this can take a minute)", name),
				); err != nil {
					return exterrors.ServiceFromAzure(err, exterrors.OpCognitiveDeploymentDelete)
				}
			}
		}
	}
	return nil
}

// purgeCognitiveAccounts purges each captured soft-deleted account. Called
// AFTER the RG is deleted so the accounts are in the soft-deleted state
// when BeginPurge runs. Hard-fails on the first error -- silently skipping
// a purge would leave a name-reservation leftover that breaks the next
// `azd provision`.
func (p *FoundryProvisioningProvider) purgeCognitiveAccounts(
	ctx context.Context,
	progress grpcbroker.ProgressFunc,
	accounts []purgeableAccount,
) error {
	deletedClient, err := armcognitiveservices.NewDeletedAccountsClient(p.subID, p.credential, nil)
	if err != nil {
		return exterrors.Internal(
			exterrors.CodeAzdClientFailed,
			fmt.Sprintf("create armcognitiveservices deleted-accounts client: %s", err),
		)
	}

	for _, a := range accounts {
		progress(fmt.Sprintf("Purging soft-deleted Cognitive Services account %s...", a.name))
		poller, err := deletedClient.BeginPurge(ctx, a.location, p.rgName, a.name, nil)
		if err != nil {
			return exterrors.ServiceFromAzure(err, exterrors.OpCognitiveAccountPurge)
		}
		if _, err := pollWithProgress(ctx, poller, progress,
			fmt.Sprintf("Purging Cognitive Services account %s (this can take a minute)", a.name),
		); err != nil {
			return exterrors.ServiceFromAzure(err, exterrors.OpCognitiveAccountPurge)
		}
	}
	return nil
}

// invalidatedEnvKeysResult returns the env keys this provider populates on
// Deploy, so azd-core can clear them after a successful Destroy.
func invalidatedEnvKeysResult() *azdext.ProvisioningDestroyResult {
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
	}
}

// Parameters reports the parameter values that will be sent to ARM, for
// `azd provision --preview` and similar tooling. The embedded path also adds
// `includeAcr`; the on-disk path skips it since the user's bicep owns the
// parameter contract there.
func (p *FoundryProvisioningProvider) Parameters(
	ctx context.Context,
) ([]*azdext.ProvisioningParameter, error) {
	out := []*azdext.ProvisioningParameter{
		{Name: "location", Value: p.location, EnvVarMapping: []string{envKeyLocation}},
		{Name: "foundryProjectName", Value: p.foundryName, EnvVarMapping: []string{envKeyProjectName}},
		{Name: "principalId", Value: p.principalID, EnvVarMapping: []string{envKeyPrincipalID}},
	}
	if p.synthResult != nil {
		out = append(out, &azdext.ProvisioningParameter{
			Name:  "includeAcr",
			Value: fmt.Sprintf("%v", p.synthResult.Parameters["includeAcr"]),
		})
	}
	return out, nil
}

// PlannedOutputs declares the outputs the ARM template emits so azd can plan
// dependent service env wiring.
func (p *FoundryProvisioningProvider) PlannedOutputs(
	ctx context.Context,
) ([]*azdext.ProvisioningPlannedOutput, error) {
	out := make([]*azdext.ProvisioningPlannedOutput, 0, len(canonicalOutputNames))
	for _, name := range canonicalOutputNames {
		out = append(out, &azdext.ProvisioningPlannedOutput{Name: name})
	}
	return out, nil
}

// canonicalOutputNames is the source of truth for the env-var names the
// foundry deployment populates. Names must match the `output <NAME>`
// declarations in internal/synthesis/templates/main.bicep (including
// AZURE_RESOURCE_GROUP, which the subscription-scoped template emits as the
// name of the resource group it creates).
//
// ARM's management SDK mangles output-name casing (e.g. AZURE_AI_PROJECT_ID
// comes back as azurE_AI_PROJECT_ID). armOutputsToProto restores the
// canonical name by case-insensitive match against this list.
var canonicalOutputNames = []string{
	"AZURE_AI_PROJECT_ID",
	"AZURE_AI_ACCOUNT_NAME",
	"AZURE_AI_PROJECT_NAME",
	"AZURE_RESOURCE_GROUP",
	"AZURE_OPENAI_ENDPOINT",
	"FOUNDRY_PROJECT_ENDPOINT",
	"AZURE_CONTAINER_REGISTRY_ENDPOINT",
	"AZURE_CONTAINER_REGISTRY_RESOURCE_ID",
	"AZURE_AI_PROJECT_ACR_CONNECTION_NAME",
}

// --- helpers ---

// deploymentsClient builds the ARM DeploymentsClient on demand, ensuring the
// credential is initialized first.
func (p *FoundryProvisioningProvider) deploymentsClient(ctx context.Context) (*armresources.DeploymentsClient, error) {
	if err := p.ensureCredential(ctx); err != nil {
		return nil, err
	}
	factory, err := armresources.NewClientFactory(p.subID, p.credential, nil)
	if err != nil {
		return nil, exterrors.Internal(
			exterrors.CodeAzdClientFailed,
			fmt.Sprintf("create armresources client: %s", err),
		)
	}
	return factory.NewDeploymentsClient(), nil
}

// deploymentName is stable per azd env so re-runs update one record.
func (p *FoundryProvisioningProvider) deploymentName() string {
	return deploymentNamePrefix + p.envName
}

// armParameters wraps the synthesizer-derived values in ARM's {"value": ...}
// envelope and merges in provider-supplied params (location, principal,
// project name). Nil-safe on p.synthResult: returns only host-derived
// parameters when Initialize hasn't run (reachable only via tests).
func (p *FoundryProvisioningProvider) armParameters() map[string]any {
	out := map[string]any{
		"location":           map[string]any{"value": p.location},
		"resourceGroupName":  map[string]any{"value": p.rgName},
		"foundryProjectName": map[string]any{"value": p.foundryName},
		"resourceTokenSalt":  map[string]any{"value": p.envName},
		"principalId":        map[string]any{"value": p.principalID},
		"tags":               map[string]any{"value": map[string]string{"azd-env-name": p.envName}},
	}
	if p.synthResult == nil {
		return out
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
		return "", exterrors.Dependency(
			exterrors.CodeProvisioningServiceNotFound,
			fmt.Sprintf("no service in azure.yaml has host in %v", FoundryServiceHosts),
			fmt.Sprintf("add a service with `host: %s` to azure.yaml", FoundryServiceHosts[0]),
		)
	case 1:
		return matches[0], nil
	default:
		return "", exterrors.Dependency(
			exterrors.CodeProvisioningServiceNotFound,
			fmt.Sprintf("multiple services declare a foundry host %v (%v); only one is supported",
				FoundryServiceHosts, matches),
			"keep a single foundry service per project",
		)
	}
}

// pollWithProgress streams a coarse "still working" heartbeat while the SDK
// poller advances.
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

// deploymentOutputs returns the Outputs map from a possibly-nil properties.
func deploymentOutputs(p *armresources.DeploymentPropertiesExtended) any {
	if p == nil {
		return nil
	}
	return p.Outputs
}

// deploymentResources returns OutputResources from a possibly-nil properties.
func deploymentResources(p *armresources.DeploymentPropertiesExtended) []*armresources.ResourceReference {
	if p == nil {
		return nil
	}
	return p.OutputResources
}

// armOutputsToProto converts an ARM Properties.Outputs map into azdext
// outputs. ARM returns each value as {type, value}; non-string values are
// JSON-encoded so they survive the round trip.
//
// Output names have their casing repaired against canonicalOutputNames (see
// that var's doc); unmatched names pass through verbatim.
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
		out[canonicalizeOutputName(k)] = &azdext.ProvisioningOutputParameter{
			Type:  typeStr,
			Value: encodeParamValue(entry["value"]),
		}
	}
	return out
}

// canonicalizeOutputName returns the canonical name matching `name`
// case-insensitively, or `name` unchanged when none matches.
func canonicalizeOutputName(name string) string {
	for _, canonical := range canonicalOutputNames {
		if strings.EqualFold(canonical, name) {
			return canonical
		}
	}
	return name
}

// armInputsToProto converts the ARM parameters map we sent into the shape
// azdext expects, JSON-encoding non-string values like the outputs converter.
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
			Value: encodeParamValue(entry["value"]),
		}
	}
	return out
}

// encodeParamValue renders an ARM parameter/output value as a wire string.
// Strings pass through; nil becomes ""; everything else is JSON-encoded so
// arrays and objects survive intact.
func encodeParamValue(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	default:
		if data, err := json.Marshal(x); err == nil {
			return string(data)
		}
		return fmt.Sprintf("%v", x)
	}
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

// isNotFound reports whether the wrapped azcore.ResponseError is a 404.
func isNotFound(err error) bool {
	respErr, ok := errors.AsType[*azcore.ResponseError](err)
	return ok && respErr.StatusCode == 404
}

// toPtr returns a pointer to its arg, for non-addressable expressions where
// new() can't be used directly.
//
//go:fix inline
func toPtr[T any](v T) *T { return new(v) }

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

// yamlUnmarshalLoose decodes YAML ignoring unknown fields, surfacing only
// real parse errors.
func yamlUnmarshalLoose(data []byte, out any) error {
	return yaml.Unmarshal(data, out)
}
