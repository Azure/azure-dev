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
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/bicep"
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
// CLI is required on the user's machine for the default path.
//
// When the user has run `azd ai agent init --infra` (or otherwise
// placed `./infra/main.bicep` or `./infra/main.bicepparam` on disk),
// the provider compiles their Bicep at runtime via azd-core's bicep
// CLI wrapper instead. The synthesizer is skipped in that mode --
// the user owns the parameter contract. See ondisk_template.go.
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

// Initialize loads azure.yaml, decides between the embedded synthesizer
// path and the on-disk Bicep path, and resolves required env values.
// It rejects brownfield (endpoint:) and missing services with
// structured errors.
//
// Initialize is "cheap" by contract: it MUST NOT do network I/O or
// build credentials. Tenant lookup and credential construction happen
// lazily in [FoundryProvisioningProvider.ensureCredential], invoked
// on-demand by Deploy/State/Destroy. The bicep CLI is similarly lazy
// (constructed only when an on-disk template actually needs to be
// compiled). azd-core may call Initialize on providers it then never
// deploys with (env refresh, multi-provider projects); making
// Initialize cheap avoids needless RPCs and lets pure metadata calls
// (Parameters, PlannedOutputs) succeed without auth.
//
// Template-source selection: if `./infra/main.bicepparam` or
// `./infra/main.bicep` exists under projectPath, the user has
// ejected (via `azd ai agent init --infra`) or hand-authored their
// own Bicep. In that mode the synthesizer is skipped entirely --
// the user owns the parameter contract. Otherwise the synthesizer
// runs and the embedded ARM JSON is loaded.
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

	// Detect on-disk Bicep before the synthesizer runs. Stat-only;
	// no compile yet (that happens lazily in resolveTemplate).
	if p.onDiskTemplatePresent() {
		log.Printf("[debug] foundry provider: on-disk Bicep detected under %s; "+
			"skipping synthesizer", filepath.Join(projectPath, onDiskInfraDir))
		// Still reject brownfield even on the on-disk path: the
		// `endpoint:` flag means "use an existing Foundry project",
		// which conflicts with a fresh ARM deployment regardless of
		// where the template came from.
		if err := rejectBrownfield(rawYAML, svcName); err != nil {
			return err
		}
		return p.resolveEnv(ctx)
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

// onDiskTemplatePresent returns true when either `infra/main.bicepparam`
// or `infra/main.bicep` exists under p.projectPath. Stat-only -- no
// content read, no compile. Mirrors the precedence in
// loadOnDiskTemplate: `.bicepparam` checked first, but for the
// presence check the answer is "yes" either way.
func (p *FoundryProvisioningProvider) onDiskTemplatePresent() bool {
	infraDir := filepath.Join(p.projectPath, onDiskInfraDir)
	return fileExistsAt(filepath.Join(infraDir, onDiskBicepParamFile)) ||
		fileExistsAt(filepath.Join(infraDir, onDiskBicepFile))
}

// rejectBrownfield is the on-disk equivalent of the synthesizer's
// ErrEndpointBrownfield branch. The synthesizer detects `endpoint:`
// on the foundry service and refuses; on the on-disk path we skip
// the synthesizer entirely, so we need a separate check to preserve
// the same refusal contract.
func rejectBrownfield(rawYAML []byte, svcName string) error {
	type svc struct {
		Endpoint string `yaml:"endpoint,omitempty"`
	}
	type root struct {
		Services map[string]svc `yaml:"services"`
	}
	var r root
	if err := yaml.Unmarshal(rawYAML, &r); err != nil {
		// Malformed yaml is surfaced upstream; skip the brownfield
		// check in that case rather than masking the parser error.
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

// resolveEnv pulls the env values the provider needs from azd-core via
// the EnvironmentService. It does NOT do any network/Azure work; that
// is deferred to ensureCredential, which Deploy/State/Destroy call
// on-demand. This split keeps Initialize cheap per its contract.
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
		// Default to the azd environment name. Most users won't customize this.
		p.foundryName = sanitizeFoundryName(p.envName)
		log.Printf("[debug] %s not set; defaulting to %q", envKeyProjectName, p.foundryName)
	}

	// Best-effort: principalId is optional. When empty, the bicep skips
	// the developer role assignment. azd core typically populates this.
	if p.principalID, _ = get(envKeyPrincipalID); p.principalID == "" {
		log.Printf("[debug] %s not set; skipping developer role assignment", envKeyPrincipalID)
	}

	return nil
}

// ensureCredential lazily looks up the subscription's tenant and
// constructs the azd-CLI credential. Safe to call repeatedly: subsequent
// calls return the cached credential. Deploy/State/Destroy invoke this
// on-demand so Initialize stays free of network I/O.
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
	client, err := p.deploymentsClient(ctx)
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
// Deploy runs an ARM deployment of the resolved template (either the
// embedded ARM JSON or the user's on-disk Bicep, depending on what
// Initialize found) with the appropriate parameter values, streaming
// progress to the caller.
func (p *FoundryProvisioningProvider) Deploy(
	ctx context.Context,
	progress grpcbroker.ProgressFunc,
) (*azdext.ProvisioningDeployResult, error) {
	progress("Preparing Foundry provisioning template...")

	if err := p.ensureResourceGroup(ctx, progress); err != nil {
		return nil, err
	}

	src, err := p.resolveTemplate(ctx, progress)
	if err != nil {
		return nil, err
	}

	dep := armresources.Deployment{
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
			Parameters: armInputsToProto(src.parameters),
			Outputs:    armOutputsToProto(deploymentOutputs(resp.Properties)),
		},
	}, nil
}

// resolveTemplate decides whether to deploy the on-disk Bicep (if
// present) or fall back to the embedded ARM JSON. Lazy: compiles
// on-disk Bicep only on first call and caches the result on the
// provider struct so re-runs within the same process skip the bicep
// CLI. Surfaces a progress line so users see which source won.
//
// On the on-disk path the user's parameters file (with ${VAR}
// substitution) is layered OVER host-derived parameters (location,
// principalId, etc.), so azd-provided values still flow through when
// the user's parameters file doesn't declare them. The user wins on
// keys present in both.
func (p *FoundryProvisioningProvider) resolveTemplate(
	ctx context.Context,
	progress grpcbroker.ProgressFunc,
) (*templateSource, error) {
	// First call: try the on-disk path.
	if p.onDiskSource == nil && p.onDiskTemplatePresent() {
		progress("Compiling on-disk Bicep templates...")
		src, err := loadOnDiskTemplate(ctx, p.projectPath, p.bicepCli(), p.envValues())
		if err != nil {
			return nil, err
		}
		if src == nil {
			// onDiskTemplatePresent said yes but the loader returned
			// nil (race with the user deleting the file mid-call).
			// Fall through to the embedded path.
			log.Printf("[debug] on-disk template disappeared between presence check and load; " +
				"falling back to embedded template")
		} else {
			p.onDiskSource = src
		}
	}

	if p.onDiskSource != nil {
		log.Printf("[debug] foundry provider: using on-disk template at %s", p.onDiskSource.sourcePath)
		// Merge: host-derived values fill gaps for parameters the
		// user's file didn't supply. User values win on collisions.
		merged := mergeParameters(p.onDiskSource.parameters, p.armParameters())
		return &templateSource{
			mode:        p.onDiskSource.mode,
			armTemplate: p.onDiskSource.armTemplate,
			parameters:  merged,
			sourcePath:  p.onDiskSource.sourcePath,
		}, nil
	}

	// Embedded path. armTemplate was loaded in Initialize.
	return &templateSource{
		mode:        templateModeEmbedded,
		armTemplate: p.armTemplate,
		parameters:  p.armParameters(),
	}, nil
}

// bicepCli lazily constructs a *bicep.Cli using azd-core's own
// download-on-demand wrapper. Calling it for the first time on a
// machine without bicep in ~/.azd/bin will trigger a download under
// a spinner; subsequent calls reuse the cached binary. We pass a
// console that writes to stdout/stderr so the spinner is visible.
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

// envValues returns the resolved name -> value map of the azd
// environment. Used for ${VAR} substitution in main.parameters.json
// and as the env passed to `bicep build-params` so its
// readEnvironmentVariable() calls resolve. Initialize-resolved
// values are surfaced under their canonical names so a user's
// ${AZURE_LOCATION} reference works even if their azd env file
// hasn't persisted them yet.
func (p *FoundryProvisioningProvider) envValues() map[string]string {
	out := map[string]string{
		envKeySubscriptionID: p.subID,
		envKeyLocation:       p.location,
		envKeyResourceGroup:  p.rgName,
		envKeyProjectName:    p.foundryName,
		envKeyPrincipalID:    p.principalID,
	}
	// Also surface the broader azd env so the user can reference
	// arbitrary AZURE_* values they set up earlier. Best-effort -- if
	// the env service is unavailable, fall back to just the canonical
	// values above.
	if p.azdClient == nil {
		return out
	}
	envClient := p.azdClient.Environment()
	if envClient == nil {
		return out
	}
	resp, err := envClient.GetValues(context.Background(), &azdext.GetEnvironmentRequest{Name: p.envName})
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

// Preview runs an ARM what-if against the resolved template (on-disk
// Bicep if present, else embedded ARM JSON) -- same template/parameter
// selection logic as Deploy, but read-only. Returns a structured diff
// summary in ProvisioningPreviewResult.Summary AND emits the same
// summary via the progress callback so the user actually sees it
// today.
//
// The progress emission is a deliberate workaround: azd-core's
// extension preview adapter currently drops the Summary field (it
// only renders Preview.Properties.Changes[], which the gRPC contract
// does not expose). Once the core proto gains a `repeated changes`
// field the Summary will be redundant; the progress emission becomes
// a confirmation line and the structured Summary takes over.
//
// Inline what-if failures (HTTP 200 with Properties.Error populated)
// are surfaced as structured CodeArmWhatIfFailed errors. Without this
// check ARM preflight failures (template validation, insufficient
// quota, etc.) would silently surface as "0 changes" and the user
// would think the preview succeeded.
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
		Properties: &armresources.DeploymentWhatIfProperties{
			Template:   src.armTemplate,
			Parameters: src.parameters,
			Mode:       toPtr(armresources.DeploymentModeIncremental),
		},
	}

	poller, err := client.BeginWhatIf(ctx, p.rgName, p.deploymentName(), whatIf, nil)
	if err != nil {
		return nil, exterrors.ServiceFromAzure(err, exterrors.OpArmDeploymentWhatIf)
	}

	resp, err := pollWithProgress(ctx, poller, progress, "What-if analysis in progress")
	if err != nil {
		return nil, exterrors.ServiceFromAzure(err, exterrors.OpArmDeploymentWhatIf)
	}

	// Inline what-if failures: ARM returns HTTP 200 but populates
	// Properties.Error. Surface as a structured error rather than
	// silently summarizing "0 changes".
	if err := whatIfFailure(resp.WhatIfOperationResult); err != nil {
		return nil, err
	}

	summary := summarizeWhatIf(resp.WhatIfOperationResult)

	// Emit the diff summary line-by-line via the progress callback so
	// the user sees it today. azd-core's extension preview adapter
	// drops the structured Summary field; this is the workaround.
	// Each progress message is a separate line in the terminal, so we
	// split the multi-line summary first.
	for line := range strings.SplitSeq(summary, "\n") {
		progress(line)
	}

	return &azdext.ProvisioningPreviewResult{
		Preview: &azdext.ProvisioningDeploymentPreview{
			Summary: summary,
		},
	}, nil
}

// Destroy tears down the Foundry deployment. Behavior depends on the
// caller-supplied flags:
//
//   - options.Force == false (default): refuse with a structured error.
//     Resource deletion is destructive and must be an explicit user
//     choice, mirroring `azd down`'s confirmation contract. The error
//     message names the resource group and points the user at
//     `azd down --force`.
//   - options.Force == true: delete the resource group (which contains
//     the Foundry account, project, and any ACR scaffolded for container
//     agents). The deployment record is removed as part of the RG
//     deletion. Re-runs are idempotent: a missing RG is a no-op success.
//
// `options.Purge` is honored for soft-deletable resources via the
// returned InvalidatedEnvKeys list (azd-core clears those env values).
// Resource-level purge of Cognitive Services soft-delete (the
// non-RG-bounded leftover) is out of scope for this provider; users who
// re-create under the same name should pass `--purge` so azd-core's
// purge pipeline handles it (when extended for extension providers).
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
			"re-run with `azd down --force` (add `--purge` to also clear "+
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

	progress(fmt.Sprintf("Deleting resource group %s...", p.rgName))
	poller, err := rgClient.BeginDelete(ctx, p.rgName, nil)
	if err != nil {
		if isNotFound(err) {
			// Already gone; treat as success so re-runs are idempotent.
			return invalidatedEnvKeysResult(), nil
		}
		return nil, exterrors.ServiceFromAzure(err, exterrors.OpResourceGroupDelete)
	}
	if _, err := pollWithProgress(ctx, poller, progress,
		fmt.Sprintf("Deleting resource group %s (this can take several minutes)", p.rgName),
	); err != nil {
		return nil, exterrors.ServiceFromAzure(err, exterrors.OpResourceGroupDelete)
	}

	return invalidatedEnvKeysResult(), nil
}

// invalidatedEnvKeysResult returns the env keys this provider populates
// on Deploy, so azd-core can clear them after a successful Destroy.
// Kept as a helper because both the "already deleted" and "just deleted"
// paths return the same list.
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

// Parameters reports the parameter values that will be sent to ARM, so
// azd can show them in `azd provision --preview` and similar tooling.
//
// Both template paths (embedded synthesizer vs on-disk Bicep) report
// the same host-derived parameter list (location, foundryProjectName,
// principalId). The embedded path also adds `includeAcr` from the
// synthesizer's derived values; the on-disk path skips it because the
// user's bicep owns the parameter contract there.
func (p *FoundryProvisioningProvider) Parameters(
	ctx context.Context,
) ([]*azdext.ProvisioningParameter, error) {
	out := []*azdext.ProvisioningParameter{
		{Name: "location", Value: p.location, EnvVarMapping: []string{envKeyLocation}},
		{Name: "foundryProjectName", Value: p.foundryName, EnvVarMapping: []string{envKeyProjectName}},
		{Name: "principalId", Value: p.principalID, EnvVarMapping: []string{envKeyPrincipalID}},
	}
	if p.synthResult != nil {
		// includeAcr is a synthesizer-derived value; only the
		// embedded path exposes it. On the on-disk path the user
		// owns the parameter contract and we don't introspect their
		// bicep here.
		out = append(out, &azdext.ProvisioningParameter{
			Name:  "includeAcr",
			Value: fmt.Sprintf("%v", p.synthResult.Parameters["includeAcr"]),
		})
	}
	return out, nil
}

// PlannedOutputs declares the outputs the embedded ARM template emits
// so azd can plan dependent service env wiring.
func (p *FoundryProvisioningProvider) PlannedOutputs(
	ctx context.Context,
) ([]*azdext.ProvisioningPlannedOutput, error) {
	out := make([]*azdext.ProvisioningPlannedOutput, 0, len(canonicalOutputNames))
	for _, name := range canonicalOutputNames {
		out = append(out, &azdext.ProvisioningPlannedOutput{Name: name})
	}
	return out, nil
}

// canonicalOutputNames is the source of truth for the env-var names
// the foundry deployment populates. Used by both PlannedOutputs (so
// azd-core knows what's coming) and armOutputsToProto (to repair
// ARM's casing). Names must match the `output <NAME>` declarations
// in `internal/synthesis/templates/main.bicep` exactly, plus the
// inputs the provider asks azd-core to remember (AZURE_RESOURCE_GROUP
// is provider-input, not a bicep output; declaring it here lets
// downstream consumers find it via the same canonical-name lookup).
//
// Why this exists: ARM's management SDK returns deployment-output
// names with mangled casing (`AZURE_AI_PROJECT_ID` comes back as
// `azurE_AI_PROJECT_ID` -- the first segment loses all-but-its-last
// letter to lowercase, then subsequent segments are intact). Without
// this list, armOutputsToProto would emit the mangled keys verbatim
// and `azd env get-value AZURE_AI_PROJECT_ID` would 404. We restore
// the canonical name by case-insensitive match.
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

// deploymentsClient builds the ARM DeploymentsClient on demand. The
// credential is lazy-initialized on the first call (see ensureCredential)
// so Initialize stays free of network I/O.
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

// ensureResourceGroup creates the configured RG if it doesn't exist.
// Re-runs are idempotent. The credential is lazy-initialized via
// ensureCredential.
func (p *FoundryProvisioningProvider) ensureResourceGroup(
	ctx context.Context,
	progress grpcbroker.ProgressFunc,
) error {
	if err := p.ensureCredential(ctx); err != nil {
		return err
	}
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
		Location: new(p.location),
		Tags:     map[string]*string{"azd-env-name": new(p.envName)},
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
//
// Nil-safe on p.synthResult: when Initialize hasn't run (a programming
// error elsewhere; reachable only via tests that bypass Initialize),
// only the host-derived parameters are returned. Deploy callers are
// guaranteed to have a non-nil synthResult by Initialize's contract.
func (p *FoundryProvisioningProvider) armParameters() map[string]any {
	out := map[string]any{
		"location":           map[string]any{"value": p.location},
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
// any-typed map keyed by output name) into azdext outputs. ARM returns
// each value as {type, value} where value may be any JSON-shaped
// scalar/array/object; we preserve the type marker and encode non-string
// values as JSON so downstream consumers can parse them back.
//
// Casing repair: ARM's management SDK returns output names with
// mangled casing (e.g. `AZURE_AI_PROJECT_ID` -> `azurE_AI_PROJECT_ID`).
// We restore the canonical name via case-insensitive lookup against
// canonicalOutputNames so `azd env get-value AZURE_AI_PROJECT_ID`
// (and every other downstream consumer that does a case-sensitive
// env lookup) finds the value. Outputs whose names don't match any
// canonical entry pass through verbatim -- we'd rather emit a
// possibly-mangled key than silently drop an output we didn't
// anticipate.
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

// canonicalizeOutputName returns the canonical (PlannedOutputs) name
// that matches `name` case-insensitively, or `name` unchanged when no
// canonical name matches. Centralized so the State, Deploy, and any
// future call sites all repair the casing consistently.
func canonicalizeOutputName(name string) string {
	for _, canonical := range canonicalOutputNames {
		if strings.EqualFold(canonical, name) {
			return canonical
		}
	}
	return name
}

// armInputsToProto converts the ARM parameters map we sent into the
// shape azdext expects for the deployment result. Like the outputs
// converter, non-string values are JSON-encoded rather than collapsed
// via fmt.Sprintf("%v", ...).
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

// encodeParamValue renders an ARM parameter/output value as a string
// suitable for the gRPC wire. Strings pass through unchanged; nil
// becomes the empty string. Everything else is JSON-encoded so arrays
// and objects survive the round trip intact (Go's default %v collapses
// `["a","b"]` to `[a b]`, which is unparseable downstream).
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
		// Fall back to %v only when JSON marshaling fails (unreachable
		// for ARM-shaped values, which are always JSON-compatible).
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

// isNotFound true if the wrapped azcore.ResponseError is a 404.
func isNotFound(err error) bool {
	respErr, ok := errors.AsType[*azcore.ResponseError](err)
	return ok && respErr.StatusCode == 404
}

// toPtr returns a pointer to its arg. Go 1.26's new() works for values
// but not when we need a typed pointer to a non-addressable expression.
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

// yamlUnmarshalLoose decodes YAML ignoring unknown fields. The default
// go-yaml behavior already ignores unknowns; this wrapper exists so we
// only surface real parse errors.
func yamlUnmarshalLoose(data []byte, out any) error {
	return yaml.Unmarshal(data, out)
}
