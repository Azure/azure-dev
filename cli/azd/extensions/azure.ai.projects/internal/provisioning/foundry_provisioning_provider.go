// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"log"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"azure.ai.projects/internal/exterrors"
	"azure.ai.projects/internal/synthesis"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices/v2"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/environment/azdcontext"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/foundry"
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
	envKeyFoundryRG      = "AZURE_FOUNDRY_RESOURCE_GROUP"
	envKeyFoundryRGOwner = "AZD_FOUNDRY_RESOURCE_GROUP_ID"
	envKeyTenantID       = "AZURE_TENANT_ID"
	envKeyProjectName    = "AZURE_AI_PROJECT_NAME"
	envKeyPrincipalID    = "AZURE_PRINCIPAL_ID"
)

const (
	// deploymentNamePrefix is prepended to the azd environment name so re-runs
	// update the same ARM deployment record.
	deploymentNamePrefix    = "azd-foundry-"
	maxDeploymentNameLength = 64
)

// FoundryProvisioningProvider implements azdext.ProvisioningProvider for
// the service whose host is FoundryProjectHost. By default it deploys
// the extension's pre-compiled ARM template (no bicep CLI required). When
// the configured <path>/<module>.bicep or .bicepparam exists on disk (e.g.
// after `azd ai agent init --infra`), it compiles that Bicep at runtime instead
// and the user owns the parameter contract. See ondisk_template.go.
type FoundryProvisioningProvider struct {
	azdClient *azdext.AzdClient

	// Populated by Initialize.
	projectPath                 string
	infraPath                   string
	infraModule                 string
	isLayer                     bool
	virtualEnv                  map[string]string
	resolvedDeploymentEnv       map[string]string
	synthResult                 *synthesis.Result
	rawAzureYAML                []byte
	serviceName                 string
	serviceEnvironments         map[string]map[string]string
	connectionEnvironmentScopes map[string]bool
	envName                     string
	subID                       string
	location                    string
	rgName                      string
	rgExplicit                  bool // active resource-group env key came from env, not the default
	foundryRGOwnerID            string
	foundryName                 string
	principalID                 string
	credential                  azcore.TokenCredential
	tenantID                    string          // resolved lazily by ensureCredential; surfaced as AZURE_TENANT_ID
	armTemplate                 map[string]any  // embedded ARM JSON; nil when onDiskSource is set
	onDiskSource                *templateSource // non-nil when configured on-disk Bicep exists

	// brownfieldEndpoint is the existing project endpoint when the foundry
	// service sets endpoint: (bring-your-own). When non-empty the provider skips
	// provisioning and connects to that project instead of creating a new one.
	brownfieldEndpoint            string
	existingProjectConnectionOnly bool
	existingProjectID             string
	existingAcrMode               string
	existingAcrEndpoint           string
	existingAcrResourceID         string
	existingAcrConnectionName     string
	existingAcrPullAssigned       bool
	resourceTokenSalt             string
	resourceGroupState            func(context.Context) (map[string]*string, bool, error)

	// Lazily constructed on first compile. nil until needed.
	bicepCliInstance bicepCompiler
}

func readProjectFile(projectRoot string) ([]byte, string, error) {
	for _, name := range azdcontext.ProjectFileNames {
		path := filepath.Join(projectRoot, name)
		// projectRoot is supplied by azd as the user's project root.
		data, err := os.ReadFile(path) //nolint:gosec
		if err == nil {
			return data, path, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, path, fmt.Errorf("read %s: %w", path, err)
		}
	}
	return nil, "", nil
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
// Initialize is cheap and performs no Azure network I/O.
// Credentials are created lazily by ensureCredential.
// The bicep CLI is built only when an on-disk template needs it.
// azd-core may initialize providers it never deploys with, so this
// keeps metadata calls unauthenticated.
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
	projectRoot, err := filepath.Abs(projectPath)
	if err != nil {
		return exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			fmt.Sprintf("resolve project path %q: %s", projectPath, err),
			"verify the project path and Foundry infrastructure layer path",
		)
	}
	projectRoot = filepath.Clean(projectRoot)
	p.projectPath = projectRoot
	configuredInfraPath := strings.TrimSpace(options.GetPath())
	if configuredInfraPath == "" {
		configuredInfraPath = onDiskInfraDir
	}
	if filepath.IsAbs(configuredInfraPath) {
		return exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			fmt.Sprintf("Foundry infrastructure path %q must be project-relative", options.GetPath()),
			"set infra.layers[].path to a project-relative directory",
		)
	}
	if slices.Contains(strings.FieldsFunc(filepath.ToSlash(configuredInfraPath), func(r rune) bool { return r == '/' }), "..") {
		return exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			fmt.Sprintf("Foundry infrastructure path %q must not contain '..'", options.GetPath()),
			"set infra.layers[].path to a directory inside the project",
		)
	}
	infraPath := filepath.Join(projectRoot, configuredInfraPath)
	relInfraPath, err := filepath.Rel(projectRoot, infraPath)
	if err != nil || relInfraPath == "." || relInfraPath == ".." ||
		strings.HasPrefix(relInfraPath, ".."+string(filepath.Separator)) {
		return exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			fmt.Sprintf("Foundry infrastructure path %q must be inside the project", options.GetPath()),
			"set infra.layers[].path to a project-relative directory",
		)
	}
	rootReal, rootErr := filepath.EvalSymlinks(projectRoot)
	existingPath := infraPath
	for {
		if _, statErr := os.Lstat(existingPath); statErr == nil {
			break
		} else if !errors.Is(statErr, os.ErrNotExist) {
			return exterrors.Validation(
				exterrors.CodeInvalidServiceConfig,
				fmt.Sprintf("inspect Foundry infrastructure path %q: %s", options.GetPath(), statErr),
				"verify the Foundry infrastructure layer path",
			)
		}
		parent := filepath.Dir(existingPath)
		if parent == existingPath {
			break
		}
		existingPath = parent
	}
	existingReal, existingErr := filepath.EvalSymlinks(existingPath)
	if rootErr != nil || existingErr != nil {
		return exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			fmt.Sprintf("resolve Foundry infrastructure path %q", options.GetPath()),
			"verify the project and Foundry infrastructure layer paths",
		)
	}
	relRealPath, err := filepath.Rel(rootReal, existingReal)
	if err != nil || relRealPath == ".." || strings.HasPrefix(relRealPath, ".."+string(filepath.Separator)) {
		return exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			fmt.Sprintf("Foundry infrastructure path %q escapes the project through a symbolic link", options.GetPath()),
			"set infra.layers[].path to a directory inside the project",
		)
	}
	p.infraPath = infraPath
	p.infraModule = strings.TrimSpace(options.GetModule())
	if p.infraModule == "" {
		p.infraModule = onDiskModule
	}
	if p.infraModule == "." || p.infraModule == ".." ||
		filepath.Base(p.infraModule) != p.infraModule ||
		strings.ContainsAny(p.infraModule, `/\\`) {
		return exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			fmt.Sprintf("Foundry infrastructure module %q must be a file name", p.infraModule),
			"set infra.layers[].module to a file name without path separators",
		)
	}
	if filepath.Ext(p.infraModule) != "" {
		return exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			fmt.Sprintf("Foundry infrastructure module %q must not include a file extension", p.infraModule),
			"set infra.layers[].module to the module base name",
		)
	}
	p.virtualEnv = maps.Clone(options.GetVirtualEnv())

	rawYAML, azureYamlPath, err := readProjectFile(projectRoot)
	if err != nil {
		return exterrors.Validation(
			exterrors.CodeInvalidAzureYaml,
			fmt.Sprintf("read %s: %s", azureYamlPath, err),
			"verify azure.yaml or azure.yml exists at the project root",
		)
	}
	if azureYamlPath == "" {
		return exterrors.Validation(
			exterrors.CodeInvalidAzureYaml,
			fmt.Sprintf("no azure.yaml or azure.yml found in %s", projectPath),
			"verify azure.yaml or azure.yml exists at the project root",
		)
	}
	if err := validateFoundryProviderLayers(rawYAML); err != nil {
		return err
	}
	config, err := parseFoundryInfraConfig(rawYAML)
	if err != nil {
		return exterrors.Validation(
			exterrors.CodeInvalidAzureYaml,
			fmt.Sprintf("parse azure.yaml infrastructure layers: %s", err),
			"verify azure.yaml is valid YAML",
		)
	}
	p.isLayer = config.hasFoundryLayer(strings.TrimSpace(options.GetName()))

	svcName, err := findFoundryProjectService(rawYAML)
	if err != nil {
		return err
	}
	p.rawAzureYAML = slices.Clone(rawYAML)
	p.serviceName = svcName

	// endpoint: selects the existing-project graph. Both project graphs deploy
	// at subscription scope and use the same embedded/on-disk template pipeline.
	endpoint, err := foundryServiceEndpointAtRoot(rawYAML, projectRoot, svcName)
	if err != nil {
		return exterrors.Validation(
			exterrors.CodeInvalidAzureYaml,
			fmt.Sprintf(
				"read Foundry project service configuration: %s",
				err,
			),
			"fix the project service configuration in azure.yaml",
		)
	}

	onDisk := p.onDiskTemplatePresent()
	if !onDisk && endpoint == "" {
		// Validate embedded config before any interactive prompts.
		// Pass the current env so connection conditions evaluate
		// even when payload ${VAR} refs are preserved.
		_, validationErr := synthesis.Synthesize(synthesis.Input{
			RawAzureYAML:    rawYAML,
			ServiceName:     svcName,
			AcceptedHosts:   FoundryProvisioningServiceHosts,
			Env:             p.networkEnvMap(ctx),
			PreserveVarRefs: true,
			ProjectRoot:     projectRoot,
		})
		if validationErr != nil &&
			!errors.Is(validationErr, synthesis.ErrEndpointBrownfield) {
			return foundrySynthesisError(svcName, validationErr)
		}
	}

	p.connectionEnvironmentScopes, err =
		synthesis.ConnectionEnvironmentScopes(
			rawYAML,
			projectRoot,
			p.networkEnvMap(ctx),
		)
	if err != nil {
		return exterrors.Validation(
			exterrors.CodeInvalidAzureYaml,
			fmt.Sprintf(
				"read Foundry connection service configuration: %s",
				err,
			),
			"fix the connection service configuration in azure.yaml",
		)
	}
	if endpoint != "" && !onDisk {
		connectionOnlyResult, synthErr := synthesis.SynthesizeExistingProject(synthesis.Input{
			RawAzureYAML:              rawYAML,
			ServiceName:               svcName,
			AcceptedHosts:             FoundryProvisioningServiceHosts,
			Env:                       p.networkEnvMap(ctx),
			PreserveDeploymentVarRefs: true,
			ProjectRoot:               projectRoot,
		})
		if synthErr != nil {
			return foundrySynthesisError(svcName, synthErr)
		}
		if !existingProjectHasMutations(connectionOnlyResult) {
			p.brownfieldEndpoint = endpoint
			p.existingProjectConnectionOnly = true
			p.synthResult = connectionOnlyResult
			p.foundryName = projectNameFromEndpoint(endpoint)
			return p.resolveEnvName(ctx)
		}
	}
	// Resolve the environment before reading service values. azd core
	// expands ${VAR} in service env against the environment, so
	// reading them first would capture empty strings for values the
	// user is about to be prompted for, and connection synthesis
	// would provision those empty strings.
	err = p.resolveEnv(ctx)
	if err != nil {
		return err
	}
	if endpoint != "" {
		if err := p.resolveExistingProjectResourceGroup(ctx); err != nil {
			return err
		}
	}

	p.serviceEnvironments, err = p.projectServiceEnvironments(ctx)
	if err != nil {
		return err
	}

	input := synthesis.Input{
		RawAzureYAML:              rawYAML,
		ServiceName:               svcName,
		AcceptedHosts:             FoundryProvisioningServiceHosts,
		Env:                       p.networkEnvMap(ctx),
		ServiceEnvironments:       p.serviceEnvironments,
		PreserveDeploymentVarRefs: true,
		ProjectRoot:               projectRoot,
	}
	var res *synthesis.Result
	if endpoint != "" {
		if err := warnNetworkIgnoredInBrownfield(
			rawYAML,
			projectRoot,
			svcName,
		); err != nil {
			return exterrors.Validation(
				exterrors.CodeInvalidAzureYaml,
				fmt.Sprintf("resolve Foundry service configuration: %s", err),
				"fix the project service configuration in azure.yaml",
			)
		}
		p.brownfieldEndpoint = endpoint
		res, err = synthesis.SynthesizeExistingProject(input)
	} else {
		res, err = synthesis.Synthesize(input)
	}
	if err != nil {
		return foundrySynthesisError(svcName, err)
	}
	p.synthResult = res
	if endpoint != "" {
		if err := p.resolveExistingProjectInputs(ctx); err != nil {
			return err
		}
	}
	if onDisk {
		log.Printf("[debug] foundry provider: on-disk Bicep detected under %s", p.infraPath)
		return nil
	}

	var tmplBytes []byte
	if endpoint != "" {
		tmplBytes, err = synthesis.ExistingProjectARMTemplate()
	} else {
		tmplBytes, err = synthesis.ARMTemplate()
	}
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

	return nil
}

// prepareProvisioning reconciles canonical deployment environment values and
// builds the resolved synthesis used by preview and deploy.
func (p *FoundryProvisioningProvider) prepareProvisioning(
	ctx context.Context,
) error {
	if p.existingProjectConnectionOnly || len(p.rawAzureYAML) == 0 ||
		p.serviceName == "" {
		return nil
	}

	if err := p.reconcileDeploymentEnvironment(
		ctx,
		p.rawAzureYAML,
		p.serviceName,
	); err != nil {
		return err
	}

	input := synthesis.Input{
		RawAzureYAML:        p.rawAzureYAML,
		ServiceName:         p.serviceName,
		AcceptedHosts:       FoundryProvisioningServiceHosts,
		Env:                 p.networkEnvMap(ctx),
		ServiceEnvironments: p.serviceEnvironments,
		ProjectRoot:         p.projectPath,
	}

	var (
		result *synthesis.Result
		err    error
	)
	if p.brownfieldEndpoint != "" {
		result, err = synthesis.SynthesizeExistingProject(input)
	} else {
		result, err = synthesis.Synthesize(input)
	}
	if err != nil {
		return foundrySynthesisError(p.serviceName, err)
	}
	p.synthResult = result
	return nil
}

func existingProjectHasMutations(result *synthesis.Result) bool {
	includeAcr, _ := result.Parameters["includeAcr"].(bool)
	deployments, _ := result.Parameters["deployments"].([]synthesis.Deployment)
	connections, _ := result.Parameters["connections"].([]synthesis.Connection)
	return includeAcr || len(deployments) > 0 || len(connections) > 0
}

func (p *FoundryProvisioningProvider) resolveExistingProjectResourceGroup(ctx context.Context) error {
	value, err := p.envValue(ctx, envKeyFoundryRG)
	if err != nil {
		return exterrors.Dependency(
			exterrors.CodeEnvironmentValuesFailed,
			fmt.Sprintf("read %s from azd environment %q: %s", envKeyFoundryRG, p.envName, err),
			"verify the azd environment is accessible, then retry",
		)
	}
	p.rgName = value
	p.rgExplicit = value != ""
	if p.rgName == "" {
		p.rgName = defaultResourceGroupName(p.envName) + "-foundry"
	}
	owner, err := p.envValue(ctx, envKeyFoundryRGOwner)
	if err != nil {
		return err
	}
	p.foundryRGOwnerID = owner
	return nil
}

func (p *FoundryProvisioningProvider) resolveExistingProjectInputs(ctx context.Context) error {
	projectID, err := p.envValue(ctx, "AZURE_AI_PROJECT_ID")
	if err != nil || projectID == "" {
		return exterrors.Dependency(
			exterrors.CodeInvalidServiceConfig,
			"AZURE_AI_PROJECT_ID is required for an existing Foundry project",
			"re-run `azd ai agent init` against the existing project, or set AZURE_AI_PROJECT_ID",
		)
	}
	resourceID, err := arm.ParseResourceID(projectID)
	if err != nil || resourceID.Parent == nil || resourceID.ResourceType.String() !=
		"Microsoft.CognitiveServices/accounts/projects" {
		return exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			fmt.Sprintf("AZURE_AI_PROJECT_ID %q is not a Foundry project resource ID", projectID),
			"set AZURE_AI_PROJECT_ID to /subscriptions/<sub>/resourceGroups/<rg>/providers/"+
				"Microsoft.CognitiveServices/accounts/<account>/projects/<project>",
		)
	}
	p.existingProjectID = projectID
	endpointAccount, endpointProject := existingProjectEndpointIdentity(p.brownfieldEndpoint)
	if endpointAccount == "" || endpointProject == "" ||
		!strings.EqualFold(endpointAccount, resourceID.Parent.Name) ||
		!strings.EqualFold(endpointProject, resourceID.Name) {
		return exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			"AZURE_AI_PROJECT_ID does not identify the project configured by the azure.yaml endpoint",
			"re-run `azd ai agent init` against the configured existing project",
		)
	}
	envEndpoint, err := p.envValue(ctx, "FOUNDRY_PROJECT_ENDPOINT")
	if err != nil {
		return fmt.Errorf("read FOUNDRY_PROJECT_ENDPOINT: %w", err)
	}
	if envEndpoint == "" || !sameExistingProjectEndpoint(p.brownfieldEndpoint, envEndpoint) {
		return exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			"FOUNDRY_PROJECT_ENDPOINT does not match the existing project configured in azure.yaml",
			"re-run `azd ai agent init` against the configured existing project",
		)
	}
	inputs := []struct {
		key   string
		value *string
	}{
		{"AZURE_CONTAINER_REGISTRY_ENDPOINT", &p.existingAcrEndpoint},
		{"AZURE_CONTAINER_REGISTRY_RESOURCE_ID", &p.existingAcrResourceID},
		{"AZURE_AI_PROJECT_ACR_CONNECTION_NAME", &p.existingAcrConnectionName},
		{"AZD_FOUNDRY_ACR_MODE", &p.existingAcrMode},
		{"AZD_RESOURCE_TOKEN_SALT", &p.resourceTokenSalt},
	}
	acrPullAssigned, err := p.envValue(ctx, "AZD_FOUNDRY_ACR_PULL_ASSIGNED")
	if err != nil {
		return fmt.Errorf("read AZD_FOUNDRY_ACR_PULL_ASSIGNED: %w", err)
	}
	p.existingAcrPullAssigned = strings.EqualFold(acrPullAssigned, "true")
	for _, input := range inputs {
		value, err := p.envValue(ctx, input.key)
		if err != nil {
			return fmt.Errorf("read %s: %w", input.key, err)
		}
		*input.value = value
	}
	includeAcr, _ := p.synthResult.Parameters["includeAcr"].(bool)
	if !includeAcr || strings.EqualFold(p.virtualEnv["AZD_AGENT_SKIP_ACR"], "true") {
		p.existingAcrMode = "none"
	} else if p.existingAcrMode == "" {
		p.existingAcrMode = p.inferExistingProjectAcrMode()
	}
	switch p.existingAcrMode {
	case "none", "create":
	case "reuse-connect", "already-connected":
		if p.existingAcrEndpoint == "" || p.existingAcrResourceID == "" {
			return exterrors.Validation(
				exterrors.CodeInvalidServiceConfig,
				fmt.Sprintf("%s requires both container registry endpoint and resource ID", p.existingAcrMode),
				"re-run `azd ai agent init` to select the container registry",
			)
		}
	default:
		return exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			fmt.Sprintf("AZD_FOUNDRY_ACR_MODE has unsupported value %q", p.existingAcrMode),
			"re-run `azd ai agent init` to select the container registry behavior",
		)
	}
	if p.existingAcrMode == "already-connected" && p.existingAcrConnectionName == "" {
		return exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			"already-connected requires AZURE_AI_PROJECT_ACR_CONNECTION_NAME",
			"re-run `azd ai agent init` and select the existing project connection",
		)
	}
	return nil
}

func (p *FoundryProvisioningProvider) inferExistingProjectAcrMode() string {
	includeAcr := false
	if p.synthResult != nil {
		includeAcr, _ = p.synthResult.Parameters["includeAcr"].(bool)
	}
	if !includeAcr || strings.EqualFold(p.virtualEnv["AZD_AGENT_SKIP_ACR"], "true") {
		return "none"
	}
	if p.existingAcrEndpoint == "" && p.existingAcrResourceID == "" {
		return "create"
	}
	if p.existingAcrConnectionName == "" {
		return "reuse-connect"
	}
	return "already-connected"
}

func foundrySynthesisError(serviceName string, err error) error {
	if errors.Is(err, synthesis.ErrServiceNotFound) {
		return exterrors.Dependency(
			exterrors.CodeProvisioningServiceNotFound,
			fmt.Sprintf(
				"no service in azure.yaml has host in %v",
				FoundryProjectServiceHosts,
			),
			fmt.Sprintf(
				"add a service with `host: %s` to azure.yaml",
				FoundryProjectHost,
			),
		)
	}
	return exterrors.Validation(
		exterrors.CodeInvalidAzureYaml,
		fmt.Sprintf(
			"synthesize foundry project service %q: %s",
			serviceName,
			err,
		),
		"check the endpoint, deployments, and network fields "+
			"under your azure.ai.project service",
	)
}

// networkEnvMap returns a best-effort name -> value map of the azd environment
// for ${VAR} substitution in network fields during synthesis. It does not
// require resolveEnv to have run; on any failure it returns nil and the
// synthesizer falls back to the process environment.
func (p *FoundryProvisioningProvider) networkEnvMap(ctx context.Context) map[string]string {
	out := make(map[string]string,
		len(p.virtualEnv)+len(p.resolvedDeploymentEnv))
	maps.Copy(out, p.resolvedDeploymentEnv)
	for key, value := range p.virtualEnv {
		if !isCanonicalDeploymentEnvironmentKey(key) {
			out[key] = value
		}
	}
	if p.azdClient == nil {
		log.Printf("[debug] foundry provider: no azd client; network ${VAR} uses process env only")
		return out
	}

	envClient := p.azdClient.Environment()
	if envClient == nil {
		log.Printf("[debug] foundry provider: no environment client; network ${VAR} uses process env only")
		return out
	}
	curr, err := envClient.GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil || curr.GetEnvironment() == nil {
		log.Printf("[debug] foundry provider: no current azd environment (%v); "+
			"network ${VAR} uses process env only", err)
		return out
	}
	resp, err := envClient.GetValues(ctx, &azdext.GetEnvironmentRequest{Name: curr.GetEnvironment().GetName()})
	if err != nil {
		log.Printf("[debug] foundry provider: GetValues failed (%s); network ${VAR} uses process env only", err)
		return out
	}
	for _, kv := range resp.GetKeyValues() {
		if kv != nil {
			if isCanonicalDeploymentEnvironmentKey(kv.Key) {
				if _, resolved := p.resolvedDeploymentEnv[kv.Key]; !resolved {
					out[kv.Key] = kv.Value
				}
			} else if _, planned := out[kv.Key]; !planned {
				out[kv.Key] = kv.Value
			}
		}
	}
	return out
}

// projectServiceEnvironments reads core-expanded service values.
// It keeps service scopes separate for connection synthesis.
func (p *FoundryProvisioningProvider) projectServiceEnvironments(
	ctx context.Context,
) (map[string]map[string]string, error) {
	if p.azdClient == nil {
		return nil, exterrors.Dependency(
			exterrors.CodeAzdClientFailed,
			"read project service environments: azd client is unavailable",
			"restart azd and retry",
		)
	}

	response, err := p.azdClient.Project().Get(
		ctx,
		&azdext.EmptyRequest{},
	)
	if err != nil {
		return nil, exterrors.Dependency(
			exterrors.CodeAzdClientFailed,
			fmt.Sprintf("read project service environments: %s", err),
			"verify the azd project is accessible, then retry",
		)
	}
	if response.GetProject() == nil {
		return nil, exterrors.Internal(
			exterrors.CodeInvalidServiceConfig,
			"read project service environments: project is missing",
		)
	}

	environments := map[string]map[string]string{}
	for name, service := range response.GetProject().GetServices() {
		if len(service.GetEnvironment()) > 0 {
			environments[name] = maps.Clone(service.GetEnvironment())
		}
	}
	return environments, nil
}

// warnNetworkIgnoredInBrownfield logs a warning when a service declares both
// endpoint: (brownfield) and network:. The account's network posture is fixed
// by whoever created it, so the network: block has no effect.
func warnNetworkIgnoredInBrownfield(
	rawYAML []byte,
	projectRoot string,
	svcName string,
) error {
	type svc struct {
		Endpoint string    `yaml:"endpoint,omitempty"`
		Network  yaml.Node `yaml:"network,omitempty"`
	}
	type root struct {
		Services map[string]map[string]any `yaml:"services"`
	}
	var r root
	if err := yaml.Unmarshal(rawYAML, &r); err != nil {
		return err
	}
	values := r.Services[svcName]
	if values == nil {
		return nil
	}
	if projectRoot != "" {
		resolved, err := foundry.ResolveFileRefs(values, projectRoot)
		if err != nil {
			return err
		}
		values = resolved
	}
	data, err := yaml.Marshal(values)
	if err != nil {
		return err
	}
	var service svc
	if err := yaml.Unmarshal(data, &service); err != nil {
		return err
	}
	if strings.TrimSpace(service.Endpoint) != "" && !service.Network.IsZero() {
		log.Printf("[warn] foundry provider: service %q sets both endpoint: and network:; "+
			"network: is ignored in brownfield mode (the account's network posture is fixed)", svcName)
	}
	return nil
}

// or <module>.bicep exists under the configured layer path. Stat-only.
func (p *FoundryProvisioningProvider) onDiskTemplatePresent() bool {
	infraPath := p.onDiskInfraPath()
	module := p.onDiskModuleName()
	return fileExistsAt(filepath.Join(infraPath, module+".bicepparam")) ||
		fileExistsAt(filepath.Join(infraPath, module+".bicep"))
}

// onDiskInfraPath returns the path supplied for the active provisioning layer,
// falling back to the legacy root infra directory for older callers and tests.
func (p *FoundryProvisioningProvider) onDiskInfraPath() string {
	if p.infraPath != "" {
		return p.infraPath
	}
	return filepath.Join(p.projectPath, onDiskInfraDir)
}

// onDiskModuleName returns the active layer module or the legacy main module.
func (p *FoundryProvisioningProvider) onDiskModuleName() string {
	if p.infraModule != "" {
		return p.infraModule
	}
	return onDiskModule
}

func foundryServiceEndpointAtRoot(
	rawYAML []byte,
	projectRoot string,
	svcName string,
) (string, error) {
	type svc struct {
		Endpoint string `yaml:"endpoint,omitempty"`
	}
	type root struct {
		Services map[string]map[string]any `yaml:"services"`
	}
	var r root
	if err := yaml.Unmarshal(rawYAML, &r); err != nil {
		return "", err
	}
	values := r.Services[svcName]
	if values == nil {
		return "", nil
	}
	if projectRoot != "" {
		resolved, err := foundry.ResolveFileRefs(
			values,
			projectRoot,
		)
		if err != nil {
			return "", err
		}
		values = resolved
	}
	data, err := yaml.Marshal(values)
	if err != nil {
		return "", err
	}
	var service svc
	if err := yaml.Unmarshal(data, &service); err != nil {
		return "", err
	}
	return strings.TrimSpace(service.Endpoint), nil
}

// defaultResourceGroupName returns the default resource group azd provisions
// into, matching azd's standard rg-<env> convention.
func defaultResourceGroupName(envName string) string {
	return "rg-" + envName
}

// withTenantOutput adds AZURE_TENANT_ID to provisioning outputs so azd persists
// it to the environment. Standard azd provision sets this value; the Foundry
// provider must too, otherwise downstream steps that need the tenant (e.g. the
// postdeploy hook) fail with "AZURE_TENANT_ID is not set in the environment".
// No-op until ensureCredential has resolved the tenant.
func (p *FoundryProvisioningProvider) withTenantOutput(
	outputs map[string]*azdext.ProvisioningOutputParameter,
) map[string]*azdext.ProvisioningOutputParameter {
	if outputs == nil {
		outputs = map[string]*azdext.ProvisioningOutputParameter{}
	}
	if p.tenantID != "" {
		if _, ok := outputs[envKeyTenantID]; !ok {
			outputs[envKeyTenantID] = &azdext.ProvisioningOutputParameter{Type: "string", Value: p.tenantID}
		}
	}
	return outputs
}

func (p *FoundryProvisioningProvider) normalizeOutputs(
	outputs map[string]*azdext.ProvisioningOutputParameter,
) map[string]*azdext.ProvisioningOutputParameter {
	trackSupportingResourceGroup := p.isLayer ||
		(p.brownfieldEndpoint != "" && p.existingAcrMode == "create")
	if trackSupportingResourceGroup {
		if outputs == nil {
			outputs = map[string]*azdext.ProvisioningOutputParameter{}
		}
		if p.isLayer {
			delete(outputs, envKeyResourceGroup)
		}
		outputs[envKeyFoundryRGOwner] = &azdext.ProvisioningOutputParameter{
			Type: "string", Value: p.foundryRGOwnerID,
		}
	}
	return outputs
}

// projectNameFromEndpoint extracts the project name from a Foundry project
// endpoint of the form https://<account>.services.ai.azure.com/api/projects/<name>.
// Returns "" when the path does not carry a project segment.
func projectNameFromEndpoint(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil {
		return ""
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	for i := 0; i+1 < len(parts); i++ {
		if parts[i] == "projects" {
			return parts[i+1]
		}
	}
	return ""
}

func existingProjectEndpointIdentity(endpoint string) (string, string) {
	u, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil {
		return "", ""
	}
	const hostSuffix = ".services.ai.azure.com"
	host := strings.ToLower(u.Hostname())
	if !strings.HasSuffix(host, hostSuffix) {
		return "", ""
	}
	return strings.TrimSuffix(host, hostSuffix), projectNameFromEndpoint(endpoint)
}

func sameExistingProjectEndpoint(a, b string) bool {
	aAccount, aProject := existingProjectEndpointIdentity(a)
	bAccount, bProject := existingProjectEndpointIdentity(b)
	return aAccount != "" && aProject != "" &&
		strings.EqualFold(aAccount, bAccount) && strings.EqualFold(aProject, bProject)
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
		if value := strings.TrimSpace(p.virtualEnv[key]); value != "" {
			return value, nil
		}
		resp, err := envClient.GetValue(ctx, &azdext.GetEnvRequest{
			EnvName: p.envName,
			Key:     key,
		})
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(resp.Value), nil
	}

	// A read error (env not found / corrupted / transport) is distinct from a
	// key that is simply unset: GetValue returns ("", nil) for an absent key.
	// Surface read failures; only prompt when the value is present-but-empty.
	if p.subID, err = get(envKeySubscriptionID); err != nil {
		return exterrors.Dependency(
			exterrors.CodeEnvironmentValuesFailed,
			fmt.Sprintf("read %s from azd environment %q: %s", envKeySubscriptionID, p.envName, err),
			"verify the azd environment is accessible, then retry",
		)
	}
	if p.subID == "" {
		// Not set yet: prompt for a subscription (matching core `azd up`) and
		// persist it, instead of failing. Under `--no-prompt` this surfaces an
		// actionable "run `azd env set ...`" error so CI/scripts stay deterministic.
		if err := p.promptSubscription(ctx); err != nil {
			return err
		}
	}

	if p.location, err = get(envKeyLocation); err != nil {
		return exterrors.Dependency(
			exterrors.CodeEnvironmentValuesFailed,
			fmt.Sprintf("read %s from azd environment %q: %s", envKeyLocation, p.envName, err),
			"verify the azd environment is accessible, then retry",
		)
	}
	if p.location == "" {
		// Not set yet: prompt for a location and persist it, instead of failing.
		if err := p.promptLocation(ctx); err != nil {
			return err
		}
	}

	rgKey := envKeyResourceGroup
	if p.isLayer {
		rgKey = envKeyFoundryRG
	}
	if p.rgName, err = get(rgKey); err != nil {
		return exterrors.Dependency(
			exterrors.CodeEnvironmentValuesFailed,
			fmt.Sprintf("read %s from azd environment %q: %s", rgKey, p.envName, err),
			"verify the azd environment is accessible, then retry",
		)
	}
	if p.rgName == "" {
		// Default to rg-<env>, matching azd's standard Bicep provisioning,
		// instead of failing. The subscription-scoped template creates the
		// resource group when it doesn't exist yet. rgExplicit stays false so
		// Destroy refuses to delete a group this provider never provisioned.
		p.rgName = defaultResourceGroupName(p.envName)
		if p.isLayer {
			p.rgName += "-foundry"
		}
		log.Printf("[debug] %s not set; defaulting to %q", rgKey, p.rgName)
	} else {
		p.rgExplicit = true
	}
	if p.isLayer {
		if p.foundryRGOwnerID, err = get(envKeyFoundryRGOwner); err != nil {
			return exterrors.Dependency(
				exterrors.CodeEnvironmentValuesFailed,
				fmt.Sprintf("read %s from azd environment %q: %s", envKeyFoundryRGOwner, p.envName, err),
				"verify the azd environment is accessible, then retry",
			)
		}
	}

	if p.foundryName, err = get(envKeyProjectName); err != nil {
		return exterrors.Dependency(
			exterrors.CodeEnvironmentValuesFailed,
			fmt.Sprintf("read %s from azd environment %q: %s", envKeyProjectName, p.envName, err),
			"verify the azd environment is accessible, then retry",
		)
	}
	if p.foundryName == "" {
		// Default to the azd environment name.
		p.foundryName = sanitizeFoundryName(p.envName)
		log.Printf("[debug] %s not set; defaulting to %q", envKeyProjectName, p.foundryName)
	}

	// principalId is optional; when empty the bicep skips the developer role assignment.
	if p.principalID, err = get(envKeyPrincipalID); err != nil {
		return exterrors.Dependency(
			exterrors.CodeEnvironmentValuesFailed,
			fmt.Sprintf("read %s from azd environment %q: %s", envKeyPrincipalID, p.envName, err),
			"verify the azd environment is accessible, then retry",
		)
	}
	if p.principalID == "" {
		log.Printf("[debug] %s not set; skipping developer role assignment", envKeyPrincipalID)
	}

	return nil
}

func (p *FoundryProvisioningProvider) resolveEnvName(ctx context.Context) error {
	currEnv, err := p.azdClient.Environment().GetCurrent(ctx, &azdext.EmptyRequest{})
	if err != nil || currEnv.GetEnvironment().GetName() == "" {
		message := "current azd environment is empty"
		if err != nil {
			message = err.Error()
		}
		return exterrors.Dependency(
			exterrors.CodeEnvironmentNotFound,
			fmt.Sprintf("get current azd environment: %s", message),
			"run 'azd env new' to create an environment",
		)
	}
	p.envName = currEnv.GetEnvironment().GetName()
	return nil
}

// promptSubscription asks the user to select an Azure subscription when
// AZURE_SUBSCRIPTION_ID is not set, then persists the choice to the azd
// environment and updates p.subID. This mirrors core `azd up`, which prompts
// for a subscription instead of failing.
//
// Under `--no-prompt` the azd host returns a "prompt required" error; that case
// is surfaced as an actionable Dependency error naming the env var to set so
// headless callers stay deterministic. Tenant resolution is left to
// ensureCredential, which looks up the user access tenant from the subscription.
func (p *FoundryProvisioningProvider) promptSubscription(ctx context.Context) error {
	resp, err := p.azdClient.Prompt().PromptSubscription(ctx, &azdext.PromptSubscriptionRequest{})
	if err != nil {
		if exterrors.IsCancellation(err) {
			return exterrors.Cancelled("subscription selection was cancelled")
		}
		if exterrors.IsPromptRequired(err) {
			return exterrors.Dependency(
				exterrors.CodeMissingAzureSubscription,
				fmt.Sprintf("%s is required but not set in azd environment %q", envKeySubscriptionID, p.envName),
				fmt.Sprintf("run `azd env set %s <subscription-id>`, or run interactively to pick one",
					envKeySubscriptionID),
			)
		}
		return exterrors.Dependency(
			exterrors.CodeMissingAzureSubscription,
			fmt.Sprintf("failed to select an Azure subscription: %s", err),
			"retry, or run interactively to pick one",
		)
	}

	subID := strings.TrimSpace(resp.GetSubscription().GetId())
	if subID == "" {
		return exterrors.Dependency(
			exterrors.CodeMissingAzureSubscription,
			"subscription selection returned an empty subscription id",
			fmt.Sprintf("retry, or run `azd env set %s <subscription-id>`", envKeySubscriptionID),
		)
	}
	p.subID = subID
	return p.setEnv(ctx, envKeySubscriptionID, p.subID)
}

// promptLocation asks the user to select an Azure location when AZURE_LOCATION
// is not set, then persists the choice and updates p.location. It mirrors
// promptSubscription's cancellation and `--no-prompt` handling. The prompt is
// scoped to the resolved subscription; no region allow-list is applied, matching
// core `azd up`.
func (p *FoundryProvisioningProvider) promptLocation(ctx context.Context) error {
	resp, err := p.azdClient.Prompt().PromptLocation(ctx, &azdext.PromptLocationRequest{
		AzureContext: &azdext.AzureContext{
			Scope: &azdext.AzureScope{SubscriptionId: p.subID, TenantId: p.tenantID},
		},
	})
	if err != nil {
		if exterrors.IsCancellation(err) {
			return exterrors.Cancelled("location selection was cancelled")
		}
		if exterrors.IsPromptRequired(err) {
			return exterrors.Dependency(
				exterrors.CodeMissingAzureLocation,
				fmt.Sprintf("%s is required but not set in azd environment %q", envKeyLocation, p.envName),
				fmt.Sprintf("run `azd env set %s <region>`, or run interactively to pick one", envKeyLocation),
			)
		}
		return exterrors.Dependency(
			exterrors.CodeMissingAzureLocation,
			fmt.Sprintf("failed to select an Azure location: %s", err),
			"retry, or run interactively to pick one",
		)
	}

	location := strings.TrimSpace(resp.GetLocation().GetName())
	if location == "" {
		return exterrors.Dependency(
			exterrors.CodeMissingAzureLocation,
			"location selection returned an empty location name",
			fmt.Sprintf("retry, or run `azd env set %s <region>`", envKeyLocation),
		)
	}
	p.location = location
	return p.setEnv(ctx, envKeyLocation, p.location)
}

// setEnv persists a single key/value to the active azd environment.
func (p *FoundryProvisioningProvider) setEnv(ctx context.Context, key, value string) error {
	if _, err := p.azdClient.Environment().SetValue(ctx, &azdext.SetEnvRequest{
		EnvName: p.envName,
		Key:     key,
		Value:   value,
	}); err != nil {
		return fmt.Errorf("persist %s to azd environment %q: %w", key, p.envName, err)
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
	// Cache the tenant so Deploy/State can surface it as AZURE_TENANT_ID.
	p.tenantID = tenantResp.TenantId

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
	if p.existingProjectConnectionOnly {
		if err := p.resolveConnectionOnlyTenant(ctx); err != nil {
			return nil, err
		}
		return &azdext.ProvisioningStateResult{State: &azdext.ProvisioningState{
			Outputs: p.existingProjectConnectionOutputs(),
		}}, nil
	}
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
			Outputs:   p.normalizeOutputs(p.withTenantOutput(armOutputsToProto(deploymentOutputs(resp.Properties)))),
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
	if p.existingProjectConnectionOnly {
		if err := p.resolveConnectionOnlyTenant(ctx); err != nil {
			return nil, err
		}
		progress("Using existing Foundry project")
		return &azdext.ProvisioningDeployResult{Deployment: &azdext.ProvisioningDeployment{
			Outputs: p.existingProjectConnectionOutputs(),
		}}, nil
	}
	progress("Preparing Foundry provisioning template...")
	if err := p.prepareProvisioning(ctx); err != nil {
		return nil, err
	}

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
			Mode:       new(armresources.DeploymentModeIncremental),
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
	resourceGroupExisted := false
	trackSupportingResourceGroup := p.isLayer ||
		(p.brownfieldEndpoint != "" && p.existingProjectAcrMode(ctx) == "create")
	if trackSupportingResourceGroup && !resourceGroupIDMatches(p.foundryRGOwnerID, p.subID, p.rgName) {
		resourceGroupExisted, err = p.resourceGroupExists(ctx)
		if err != nil {
			return nil, err
		}
	}

	name := p.deploymentName()
	progress(fmt.Sprintf("Starting ARM deployment %q...", name))

	poller, err := client.BeginCreateOrUpdateAtSubscriptionScope(ctx, name, dep, nil)
	if err != nil {
		return nil, exterrors.ServiceFromAzure(err, exterrors.OpArmDeploymentCreate)
	}

	resp, err := pollWithProgress(ctx, poller, progress, "Foundry deployment in progress")
	if err != nil {
		if trackSupportingResourceGroup && !resourceGroupExisted {
			if ownershipErr := p.persistCreatedResourceGroupOwnership(ctx); ownershipErr != nil {
				log.Printf("[debug] recover Foundry resource-group ownership after deployment failure: %v", ownershipErr)
			}
		}
		return nil, exterrors.ServiceFromAzure(err, exterrors.OpArmDeploymentCreate)
	}

	progress("Foundry deployment complete")
	if trackSupportingResourceGroup {
		p.foundryRGOwnerID = resolveLayerResourceGroupOwnership(
			p.foundryRGOwnerID, p.subID, p.rgName, resourceGroupExisted, resp.Properties)
		if p.foundryRGOwnerID == "" && !resourceGroupExisted {
			if err := p.persistCreatedResourceGroupOwnership(ctx); err != nil {
				return nil, err
			}
		}
	}

	return &azdext.ProvisioningDeployResult{
		Deployment: &azdext.ProvisioningDeployment{
			Parameters: armInputsToProto(src.parameters),
			Outputs:    p.normalizeOutputs(p.withTenantOutput(armOutputsToProto(deploymentOutputs(resp.Properties)))),
		},
	}, nil
}

func (p *FoundryProvisioningProvider) existingProjectConnectionOutputs() map[string]*azdext.ProvisioningOutputParameter {
	return p.withTenantOutput(map[string]*azdext.ProvisioningOutputParameter{
		"AZURE_AI_PROJECT_NAME":    {Type: "string", Value: p.foundryName},
		"FOUNDRY_PROJECT_ENDPOINT": {Type: "string", Value: p.brownfieldEndpoint},
	})
}

func (p *FoundryProvisioningProvider) resolveConnectionOnlyTenant(ctx context.Context) error {
	subscriptionID, err := p.envValue(ctx, envKeySubscriptionID)
	if err != nil {
		return fmt.Errorf("read %s: %w", envKeySubscriptionID, err)
	}
	if subscriptionID == "" {
		tenantID, err := p.envValue(ctx, envKeyTenantID)
		if err != nil {
			return fmt.Errorf("read %s: %w", envKeyTenantID, err)
		}
		p.tenantID = tenantID
		return nil
	}
	tenant, err := p.azdClient.Account().LookupTenant(ctx, &azdext.LookupTenantRequest{SubscriptionId: subscriptionID})
	if err != nil {
		return exterrors.Auth(
			exterrors.CodeTenantLookupFailed,
			fmt.Sprintf("look up tenant for subscription %s: %s", subscriptionID, err),
			"run 'azd auth login' and verify access to the subscription",
		)
	}
	p.tenantID = tenant.TenantId
	return nil
}

// envValue reads a single value from the active azd environment, trimmed.
func (p *FoundryProvisioningProvider) envValue(ctx context.Context, key string) (string, error) {
	if !isCanonicalDeploymentEnvironmentKey(key) {
		if value := strings.TrimSpace(p.virtualEnv[key]); value != "" {
			return value, nil
		}
	}
	resp, err := p.azdClient.Environment().GetValue(ctx, &azdext.GetEnvRequest{
		EnvName: p.envName,
		Key:     key,
	})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(resp.Value), nil
}

func (p *FoundryProvisioningProvider) envValues(ctx context.Context) map[string]string {
	out := make(map[string]string,
		len(p.virtualEnv)+len(p.resolvedDeploymentEnv)+6)
	maps.Copy(out, p.resolvedDeploymentEnv)
	maps.Copy(out, map[string]string{
		envKeySubscriptionID: p.subID,
		envKeyLocation:       p.location,
		envKeyResourceGroup:  p.rgName,
		envKeyFoundryRG:      p.rgName,
		envKeyProjectName:    p.foundryName,
		envKeyPrincipalID:    p.principalID,
	})
	for key, value := range p.virtualEnv {
		if isCanonicalDeploymentEnvironmentKey(key) {
			continue
		}
		if _, canonical := out[key]; !canonical {
			out[key] = value
		}
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
		if isCanonicalDeploymentEnvironmentKey(kv.Key) {
			if _, resolved := p.resolvedDeploymentEnv[kv.Key]; !resolved {
				out[kv.Key] = kv.Value
			}
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
		src, err := loadOnDiskTemplateAtWithEnvironment(
			ctx,
			p.onDiskInfraPath(),
			p.onDiskModuleName(),
			p.bicepCli(),
			onDiskEnvironment{
				project:           p.envValues(ctx),
				services:          p.serviceEnvironments,
				scopedConnections: p.connectionEnvironmentScopes,
			},
		)
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
		hostParameters := parametersDeclaredByTemplate(p.armParameters(), p.onDiskSource.armTemplate)
		merged := mergeParameters(p.onDiskSource.parameters, hostParameters)
		return &templateSource{
			mode:        p.onDiskSource.mode,
			armTemplate: p.onDiskSource.armTemplate,
			parameters:  merged,
			sourcePath:  p.onDiskSource.sourcePath,
		}, nil
	}

	if p.armTemplate == nil {
		// On-disk init skips synthesis, so the embedded template is never loaded.
		// If the on-disk Bicep disappeared between presence check and load there
		// is nothing to deploy; error out instead of sending an empty template.
		return nil, exterrors.Validation(
			exterrors.CodeOnDiskTemplateMissing,
			"on-disk Bicep template is no longer present and no embedded template is loaded",
			fmt.Sprintf("restore %s (or its .bicepparam file) or re-run init without --infra",
				filepath.Join(p.onDiskInfraPath(), p.onDiskModuleName()+".bicep")),
		)
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
	if p.existingProjectConnectionOnly {
		progress("Using existing Foundry project; nothing to provision")
		return &azdext.ProvisioningPreviewResult{
			Preview: &azdext.ProvisioningDeploymentPreview{},
		}, nil
	}
	progress("Computing deployment plan...")
	if err := p.prepareProvisioning(ctx); err != nil {
		return nil, err
	}

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
			Mode:       new(armresources.DeploymentModeIncremental),
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
//   - Force == false (default): prompt the user for confirmation before
//     deleting, mirroring the built-in bicep provider. Under --no-prompt (or
//     with no azd host attached) there is nothing to prompt, so deletion falls
//     back to requiring an explicit --force choice via a structured error.
//   - A named infra.layers entry uses its isolated Foundry resource group, so
//     teardown cannot remove resources owned by sibling layers.
//   - Force == true on the legacy root provider: delete every model deployment
//     under the resource group's
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
	if p.brownfieldEndpoint != "" {
		if p.existingProjectConnectionOnly {
			progress("Existing Foundry project is not owned by azd; leaving it in place")
			return &azdext.ProvisioningDestroyResult{}, nil
		}
		mode := p.existingProjectAcrMode(ctx)
		if mode == "none" || mode == "already-connected" {
			progress("Existing Foundry project resources are not owned by azd; leaving them in place")
			return &azdext.ProvisioningDestroyResult{}, nil
		}
		if mode != "create" {
			return nil, exterrors.Validation(
				exterrors.CodeInvalidServiceConfig,
				"azd down cannot safely remove resources created inside an existing Foundry project",
				"remove the declared deployments and connections from the existing project explicitly",
			)
		}
		// Only the ownership-tracked adjunct resource group can be deleted safely.
		// The reused Foundry project and its children remain user-owned.
		p.isLayer = true
	}

	// Fail closed when the active resource-group key was never set: rgName is the rg-<env>
	// default the provider made up, not a group it provisioned. Deleting it could
	// wipe an unrelated group that happens to match a common env name like "dev".
	// Check this before prompting so we never ask the user to confirm a deletion
	// we are going to refuse anyway.
	if !p.rgExplicit {
		rgKey := envKeyResourceGroup
		if p.isLayer {
			rgKey = envKeyFoundryRG
		}
		return nil, exterrors.Validation(
			exterrors.CodeMissingResourceGroup,
			fmt.Sprintf("%s is not set, so this provider has no record of a resource group "+
				"it provisioned; refusing to delete the assumed default %q", rgKey, p.rgName),
			fmt.Sprintf("set the group to delete with `azd env set %s <name>` if it was provisioned here",
				rgKey),
		)
	}
	if p.isLayer {
		if err := verifyLayerResourceGroupOwnership(p.foundryRGOwnerID, p.subID, p.rgName); err != nil {
			return nil, err
		}
		if err := p.verifyLayerResourceGroupAzureOwnership(ctx); err != nil {
			return nil, err
		}
	}

	if !options.GetForce() {
		confirmed, err := p.confirmDestroy(ctx)
		if err != nil {
			return nil, err
		}
		if !confirmed {
			return nil, exterrors.Cancelled("destroy cancelled")
		}
	}
	if err := p.ensureCredential(ctx); err != nil {
		return nil, err
	}
	if p.brownfieldEndpoint != "" {
		if err := p.deleteExistingProjectAcrConnection(ctx, progress); err != nil {
			return nil, err
		}
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
			return p.destroyResult(), nil
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

	return p.destroyResult(), nil
}

func (p *FoundryProvisioningProvider) deleteExistingProjectAcrConnection(
	ctx context.Context,
	progress grpcbroker.ProgressFunc,
) error {
	acrID, err := arm.ParseResourceID(p.existingAcrResourceID)
	if err != nil || acrID.ResourceType.String() != "Microsoft.ContainerRegistry/registries" ||
		!strings.EqualFold(acrID.SubscriptionID, p.subID) ||
		!strings.EqualFold(acrID.ResourceGroupName, p.rgName) || acrID.Name == "" {
		return exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			"AZURE_CONTAINER_REGISTRY_RESOURCE_ID does not identify a registry in the azd-owned resource group",
			"re-run `azd provision` to restore the create-mode registry state",
		)
	}
	expectedConnectionName := acrID.Name + "-conn"
	connectionName := strings.TrimSpace(p.existingAcrConnectionName)
	if connectionName != "" && !strings.EqualFold(connectionName, expectedConnectionName) {
		return exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			"AZURE_AI_PROJECT_ACR_CONNECTION_NAME does not match the azd-created container registry",
			"re-run `azd provision` to restore the create-mode registry connection state",
		)
	}
	connectionName = expectedConnectionName
	projectID, err := arm.ParseResourceID(p.existingProjectID)
	if err != nil || projectID.Parent == nil {
		return exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			"AZURE_AI_PROJECT_ID is not a valid Foundry project resource ID",
			"re-run `azd ai agent init` against the configured existing project",
		)
	}
	client, err := armcognitiveservices.NewProjectConnectionsClient(projectID.SubscriptionID, p.credential, nil)
	if err != nil {
		return exterrors.Internal(
			exterrors.CodeAzdClientFailed,
			fmt.Sprintf("create Foundry project connections client: %s", err),
		)
	}
	connection, err := client.Get(
		ctx,
		projectID.ResourceGroupName,
		projectID.Parent.Name,
		projectID.Name,
		connectionName,
		nil,
	)
	if err != nil {
		if isNotFound(err) {
			return nil
		}
		return exterrors.ServiceFromAzure(err, exterrors.OpProjectConnectionGet)
	}
	properties := connection.Properties.GetConnectionPropertiesV2()
	resourceID := ""
	if properties != nil && properties.Metadata != nil && properties.Metadata["ResourceId"] != nil {
		resourceID = *properties.Metadata["ResourceId"]
	}
	if properties == nil || properties.Category == nil ||
		*properties.Category != armcognitiveservices.ConnectionCategoryContainerRegistry ||
		properties.AuthType == nil ||
		*properties.AuthType != armcognitiveservices.ConnectionAuthTypeManagedIdentity ||
		properties.Target == nil ||
		!strings.EqualFold(strings.TrimSpace(*properties.Target), strings.TrimSpace(p.existingAcrEndpoint)) ||
		!strings.EqualFold(strings.TrimSpace(resourceID), acrID.String()) {
		return exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			fmt.Sprintf("Foundry project connection %q no longer references the azd-created registry", connectionName),
			"remove or rename the replacement connection, then retry cleanup",
		)
	}
	progress(fmt.Sprintf("Deleting Foundry project connection %s...", connectionName))
	_, err = client.Delete(
		ctx,
		projectID.ResourceGroupName,
		projectID.Parent.Name,
		projectID.Name,
		connectionName,
		nil,
	)
	if err != nil && !isNotFound(err) {
		return exterrors.ServiceFromAzure(err, exterrors.OpProjectConnectionDelete)
	}
	return nil
}

func (p *FoundryProvisioningProvider) destroyResult() *azdext.ProvisioningDestroyResult {
	if p.brownfieldEndpoint != "" {
		return &azdext.ProvisioningDestroyResult{InvalidatedEnvKeys: []string{
			"AZURE_CONTAINER_REGISTRY_ENDPOINT",
			"AZURE_CONTAINER_REGISTRY_RESOURCE_ID",
			"AZURE_AI_PROJECT_ACR_CONNECTION_NAME",
			"AZURE_FOUNDRY_RESOURCE_GROUP",
			"AZD_FOUNDRY_ACR_MODE",
			"AZD_FOUNDRY_ACR_PULL_ASSIGNED",
			envKeyFoundryRGOwner,
		}}
	}
	return invalidatedEnvKeysResult()
}

// confirmDestroy asks the user to confirm resource-group deletion when the
// caller didn't pass --force. It mirrors the built-in bicep provider, which
// prompts instead of hard-failing.
//
// Fails closed in the non-interactive cases so we never silently delete:
//   - No azd host attached (azdClient == nil): return the actionable
//     CodeDestroyRequiresForce error.
//   - Under `--no-prompt` the host returns a "prompt required" error; that is
//     surfaced as the same CodeDestroyRequiresForce error so CI/scripts stay
//     deterministic and are told to pass --force.
//
// A user cancellation (Ctrl-C) or an explicit "no" both return (false, nil) so
// the caller reports a clean cancellation rather than an error.
func (p *FoundryProvisioningProvider) confirmDestroy(ctx context.Context) (bool, error) {
	target := fmt.Sprintf("resource group %q and all resources inside it", p.rgName)
	if p.brownfieldEndpoint != "" {
		target += " plus its Container Registry connection inside the existing Foundry project"
	}
	forceRequired := exterrors.Validation(
		exterrors.CodeDestroyRequiresForce,
		fmt.Sprintf("microsoft.foundry destroy will delete %s; no interactive prompt is available, "+
			"so --force is required", target),
		"re-run with `azd down --force` (add `--purge` to also purge "+
			"soft-deleted Cognitive Services accounts)",
	)

	if p.azdClient == nil {
		return false, forceRequired
	}

	resp, err := p.azdClient.Prompt().Confirm(ctx, &azdext.ConfirmRequest{
		Options: &azdext.ConfirmOptions{
			Message: fmt.Sprintf(
				"microsoft.foundry will delete %s. Are you sure you want to continue?", target),
			DefaultValue: new(false),
		},
	})
	if err != nil {
		if exterrors.IsCancellation(err) {
			return false, nil
		}
		if exterrors.IsPromptRequired(err) {
			return false, forceRequired
		}
		return false, fmt.Errorf("prompting for destroy confirmation: %w", err)
	}

	return resp.GetValue(), nil
}

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
			"AZURE_AI_PROJECT_CONNECTION_NAMES",
			"AZURE_AI_PROJECT_CONNECTIONS_PROJECT_ENDPOINT",
			"AZURE_FOUNDRY_RESOURCE_GROUP",
			"AZD_FOUNDRY_ACR_MODE",
			"AZD_FOUNDRY_ACR_PULL_ASSIGNED",
			envKeyFoundryRGOwner,
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
	if p.brownfieldEndpoint != "" {
		return []*azdext.ProvisioningParameter{
			{Name: "projectResourceId", Value: p.existingProjectID, EnvVarMapping: []string{"AZURE_AI_PROJECT_ID"}},
			{Name: "projectEndpoint", Value: p.brownfieldEndpoint, EnvVarMapping: []string{"FOUNDRY_PROJECT_ENDPOINT"}},
			{Name: "acrMode", Value: p.existingAcrMode, EnvVarMapping: []string{"AZD_FOUNDRY_ACR_MODE"}},
		}, nil
	}
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
	names := greenfieldOutputNames
	if p.existingProjectConnectionOnly {
		names = []string{"AZURE_AI_PROJECT_NAME", "FOUNDRY_PROJECT_ENDPOINT"}
	} else if p.brownfieldEndpoint != "" {
		names = existingProjectOutputNames
	}
	out := make([]*azdext.ProvisioningPlannedOutput, 0, len(names))
	for _, name := range names {
		if p.isLayer && name == envKeyResourceGroup {
			continue
		}
		out = append(out, &azdext.ProvisioningPlannedOutput{Name: name})
	}
	return out, nil
}

// canonicalOutputNames is the source of truth for the env-var names the
// foundry deployment populates. Names must match the `output <NAME>`
// declarations in internal/synthesis/templates/main.bicep.
//
// ARM's management SDK mangles output-name casing (e.g. AZURE_AI_PROJECT_ID
// comes back as azurE_AI_PROJECT_ID). armOutputsToProto restores the
// canonical name by case-insensitive match against this list.
var canonicalOutputNames = []string{
	"AZURE_AI_PROJECT_ID",
	"AZURE_AI_ACCOUNT_NAME",
	"AZURE_AI_PROJECT_NAME",
	"AZURE_RESOURCE_GROUP",
	"AZURE_FOUNDRY_RESOURCE_GROUP",
	"AZURE_OPENAI_ENDPOINT",
	"FOUNDRY_PROJECT_ENDPOINT",
	"AZURE_CONTAINER_REGISTRY_ENDPOINT",
	"AZURE_CONTAINER_REGISTRY_RESOURCE_ID",
	"AZURE_AI_PROJECT_ACR_CONNECTION_NAME",
	"AZURE_AI_PROJECT_CONNECTION_NAMES",
	"AZURE_AI_PROJECT_CONNECTIONS_PROJECT_ENDPOINT",
	"AZURE_FOUNDRY_NETWORK_MODE",
	"AZURE_FOUNDRY_MANAGED_ISOLATION_MODE",
	"AZD_FOUNDRY_ACR_MODE",
}

var greenfieldOutputNames = []string{
	"AZURE_AI_PROJECT_ID",
	"AZURE_AI_ACCOUNT_NAME",
	"AZURE_AI_PROJECT_NAME",
	"AZURE_RESOURCE_GROUP",
	"AZURE_FOUNDRY_RESOURCE_GROUP",
	"AZURE_OPENAI_ENDPOINT",
	"FOUNDRY_PROJECT_ENDPOINT",
	"AZURE_CONTAINER_REGISTRY_ENDPOINT",
	"AZURE_CONTAINER_REGISTRY_RESOURCE_ID",
	"AZURE_AI_PROJECT_ACR_CONNECTION_NAME",
	"AZURE_AI_PROJECT_CONNECTION_NAMES",
	"AZURE_AI_PROJECT_CONNECTIONS_PROJECT_ENDPOINT",
	"AZURE_FOUNDRY_NETWORK_MODE",
	"AZURE_FOUNDRY_MANAGED_ISOLATION_MODE",
}

var existingProjectOutputNames = []string{
	"AZURE_AI_PROJECT_ID",
	"AZURE_AI_ACCOUNT_NAME",
	"AZURE_AI_PROJECT_NAME",
	"AZURE_FOUNDRY_RESOURCE_GROUP",
	"AZURE_OPENAI_ENDPOINT",
	"FOUNDRY_PROJECT_ENDPOINT",
	"AZURE_CONTAINER_REGISTRY_ENDPOINT",
	"AZURE_CONTAINER_REGISTRY_RESOURCE_ID",
	"AZURE_AI_PROJECT_ACR_CONNECTION_NAME",
	"AZURE_AI_PROJECT_CONNECTION_NAMES",
	"AZURE_AI_PROJECT_CONNECTIONS_PROJECT_ENDPOINT",
	"AZD_FOUNDRY_ACR_MODE",
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

// deploymentName is stable per azd env and capped at ARM's 64-character limit.
// The project-path hash separates projects sharing an env name; an env-name hash
// preserves that identity when the readable env-name segment must be truncated.
// Hashing the project root rather than the layer path keeps the name stable
// across a root-to-layer migration.
func (p *FoundryProvisioningProvider) deploymentName() string {
	pathHash := fnv.New32a()
	_, _ = pathHash.Write([]byte(p.projectPath))
	name := fmt.Sprintf("%s%s-%08x", deploymentNamePrefix, p.envName, pathHash.Sum32())
	if len(name) <= maxDeploymentNameLength {
		return name
	}

	envHash := fnv.New32a()
	_, _ = envHash.Write([]byte(p.envName))
	hashTail := fmt.Sprintf("-%08x-%08x", envHash.Sum32(), pathHash.Sum32())
	keep := maxDeploymentNameLength - len(deploymentNamePrefix) - len(hashTail)
	return deploymentNamePrefix + p.envName[:keep] + hashTail
}

// armParameters wraps the synthesizer-derived values in ARM's {"value": ...}
// envelope and merges in provider-supplied params (location, principal,
// project name). Nil-safe on p.synthResult: returns only host-derived
// parameters when Initialize hasn't run (reachable only via tests).
func (p *FoundryProvisioningProvider) armParameters() map[string]any {
	if p.brownfieldEndpoint != "" {
		return p.existingProjectArmParameters()
	}
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

func (p *FoundryProvisioningProvider) existingProjectArmParameters() map[string]any {
	out := map[string]any{
		"projectResourceId":         map[string]any{"value": p.existingProjectID},
		"projectEndpoint":           map[string]any{"value": p.brownfieldEndpoint},
		"resourceGroupName":         map[string]any{"value": p.rgName},
		"location":                  map[string]any{"value": p.location},
		"resourceTokenSalt":         map[string]any{"value": p.resourceTokenSalt},
		"tags":                      map[string]any{"value": map[string]string{"azd-env-name": p.envName}},
		"acrMode":                   map[string]any{"value": p.existingAcrMode},
		"existingAcrEndpoint":       map[string]any{"value": p.existingAcrEndpoint},
		"existingAcrResourceId":     map[string]any{"value": p.existingAcrResourceID},
		"existingAcrConnectionName": map[string]any{"value": p.existingAcrConnectionName},
		"acrPullAssigned":           map[string]any{"value": p.existingAcrPullAssigned},
	}
	if p.synthResult != nil {
		for k, v := range p.synthResult.Parameters {
			if k != "includeAcr" {
				out[k] = map[string]any{"value": v}
			}
		}
	}
	return out
}

func (p *FoundryProvisioningProvider) existingProjectAcrMode(_ context.Context) string {
	return p.existingAcrMode
}

// findFoundryProjectService scans azure.yaml for a single azure.ai.project service and returns its name.
func findFoundryProjectService(raw []byte) (string, error) {
	type svc struct {
		Host    string    `yaml:"host"`
		Network yaml.Node `yaml:"network,omitempty"`
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
	var misplacedNetwork []string
	for name, s := range r.Services {
		if slices.Contains(FoundryProjectServiceHosts, s.Host) {
			matches = append(matches, name)
			continue
		}
		if IsFoundryNetworkHost(s.Host) && !s.Network.IsZero() {
			misplacedNetwork = append(misplacedNetwork, name)
		}
	}
	if len(misplacedNetwork) > 0 {
		slices.Sort(misplacedNetwork)
		return "", exterrors.Validation(
			exterrors.CodeInvalidAzureYaml,
			fmt.Sprintf("network: is only supported on services with host: %s (found on %v)",
				FoundryProjectHost, misplacedNetwork),
			"move the network: block to the azure.ai.project service (for example, services.ai-project)",
		)
	}

	switch len(matches) {
	case 1:
		return matches[0], nil
	case 0:
		var legacyMatches []string
		for name, s := range r.Services {
			if slices.Contains(FoundryLegacyProvisioningHosts, s.Host) {
				legacyMatches = append(legacyMatches, name)
			}
		}
		switch len(legacyMatches) {
		case 1:
			return legacyMatches[0], nil
		case 0:
			return "", exterrors.Dependency(
				exterrors.CodeProvisioningServiceNotFound,
				fmt.Sprintf("no service in azure.yaml has host in %v", FoundryProvisioningServiceHosts),
				fmt.Sprintf("add a service with `host: %s` to azure.yaml", FoundryProjectHost),
			)
		default:
			slices.Sort(legacyMatches)
			return "", exterrors.Dependency(
				exterrors.CodeProvisioningServiceNotFound,
				fmt.Sprintf("multiple legacy services declare a foundry provisioning host %v (%v); only one is supported",
					FoundryLegacyProvisioningHosts, legacyMatches),
				"keep a single azure.ai.project service per project, or a single pre-split foundry service",
			)
		}
	default:
		slices.Sort(matches)
		return "", exterrors.Dependency(
			exterrors.CodeProvisioningServiceNotFound,
			fmt.Sprintf("multiple services declare a foundry project host %v (%v); only one is supported",
				FoundryProjectServiceHosts, matches),
			"keep a single azure.ai.project service per project",
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

func ownedLayerResourceGroupID(
	properties *armresources.DeploymentPropertiesExtended,
	subscriptionID, resourceGroup string,
) string {
	wantedID := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", subscriptionID, resourceGroup)
	for _, resource := range deploymentResources(properties) {
		if resource != nil && resource.ID != nil && strings.EqualFold(strings.TrimSuffix(*resource.ID, "/"), wantedID) {
			return wantedID
		}
	}
	return ""
}

func verifyLayerResourceGroupOwnership(ownerID, subscriptionID, resourceGroup string) error {
	if resourceGroupIDMatches(ownerID, subscriptionID, resourceGroup) {
		return nil
	}
	return layerResourceGroupOwnershipError(resourceGroup, "the azd environment does not record ownership of this group")
}

func resourceGroupIDMatches(resourceID, subscriptionID, resourceGroup string) bool {
	wantedID := fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", subscriptionID, resourceGroup)
	return resourceID != "" && strings.EqualFold(strings.TrimSuffix(resourceID, "/"), wantedID)
}

func resolveLayerResourceGroupOwnership(
	currentID, subscriptionID, resourceGroup string,
	resourceGroupExisted bool,
	properties *armresources.DeploymentPropertiesExtended,
) string {
	if resourceGroupIDMatches(currentID, subscriptionID, resourceGroup) {
		return currentID
	}
	if resourceGroupExisted {
		return ""
	}
	return ownedLayerResourceGroupID(properties, subscriptionID, resourceGroup)
}

func (p *FoundryProvisioningProvider) resourceGroupExists(ctx context.Context) (bool, error) {
	_, found, err := p.lookupResourceGroupState(ctx)
	return found, err
}

func (p *FoundryProvisioningProvider) lookupResourceGroupState(
	ctx context.Context,
) (map[string]*string, bool, error) {
	if p.resourceGroupState != nil {
		return p.resourceGroupState(ctx)
	}
	factory, err := armresources.NewClientFactory(p.subID, p.credential, nil)
	if err != nil {
		return nil, false, exterrors.Internal(
			exterrors.CodeAzdClientFailed,
			fmt.Sprintf("create armresources client for resource-group existence check: %s", err),
		)
	}
	resp, err := factory.NewResourceGroupsClient().Get(ctx, p.rgName, nil)
	if err == nil {
		return resp.Tags, true, nil
	}
	if isNotFound(err) {
		return nil, false, nil
	}
	return nil, false, exterrors.ServiceFromAzure(err, exterrors.OpResourceGroupGet)
}

func (p *FoundryProvisioningProvider) persistCreatedResourceGroupOwnership(ctx context.Context) error {
	recoveryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 30*time.Second)
	defer cancel()
	if err := p.ensureCredential(recoveryCtx); err != nil {
		return err
	}
	tags, found, err := p.lookupResourceGroupState(recoveryCtx)
	if err != nil {
		return err
	}
	if !found {
		return nil
	}
	if err := verifyLayerResourceGroupTags(tags, p.envName, p.rgName); err != nil {
		return err
	}
	if err := p.setEnv(recoveryCtx, envKeyFoundryRG, p.rgName); err != nil {
		return err
	}
	p.rgExplicit = true
	p.foundryRGOwnerID = fmt.Sprintf("/subscriptions/%s/resourceGroups/%s", p.subID, p.rgName)
	return p.setEnv(recoveryCtx, envKeyFoundryRGOwner, p.foundryRGOwnerID)
}

func (p *FoundryProvisioningProvider) verifyLayerResourceGroupAzureOwnership(ctx context.Context) error {
	if err := p.ensureCredential(ctx); err != nil {
		return err
	}
	factory, err := armresources.NewClientFactory(p.subID, p.credential, nil)
	if err != nil {
		return exterrors.Internal(
			exterrors.CodeAzdClientFailed,
			fmt.Sprintf("create armresources client for resource-group ownership check: %s", err),
		)
	}
	resp, err := factory.NewResourceGroupsClient().Get(ctx, p.rgName, nil)
	if err != nil {
		if isNotFound(err) {
			return nil
		}
		return exterrors.ServiceFromAzure(err, exterrors.OpResourceGroupGet)
	}
	return verifyLayerResourceGroupTags(resp.Tags, p.envName, p.rgName)
}

func verifyLayerResourceGroupTags(tags map[string]*string, environmentName, resourceGroup string) error {
	for key, value := range tags {
		if strings.EqualFold(key, "azd-env-name") && value != nil && *value == environmentName {
			return nil
		}
	}
	return layerResourceGroupOwnershipError(
		resourceGroup,
		fmt.Sprintf("the Azure resource group is not tagged for azd environment %q", environmentName),
	)
}

func layerResourceGroupOwnershipError(resourceGroup, reason string) error {
	return exterrors.Validation(
		exterrors.CodeMissingResourceGroup,
		fmt.Sprintf("refusing to delete Foundry layer resource group %q: %s", resourceGroup, reason),
		"restore the original Foundry deployment state or delete only resources you have verified manually",
	)
}

func validateFoundryProviderLayers(rawYAML []byte) error {
	config, err := parseFoundryInfraConfig(rawYAML)
	if err != nil {
		return exterrors.Validation(
			exterrors.CodeInvalidAzureYaml,
			fmt.Sprintf("parse azure.yaml infrastructure layers: %s", err),
			"verify azure.yaml is valid YAML",
		)
	}

	if config.hasLayers && config.rootProvider == FoundryProviderName {
		return exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			fmt.Sprintf("infra.provider is %q while infra.layers is also declared; the root Foundry provider "+
				"cannot be combined with named layers", FoundryProviderName),
			"move existing infrastructure into explicitly named layers with their own providers, then keep one "+
				"Foundry layer using microsoft.foundry",
		)
	}
	if len(config.foundryLayers) > 1 {
		return exterrors.Validation(
			exterrors.CodeInvalidServiceConfig,
			fmt.Sprintf("multiple infrastructure layers use provider %q: %s",
				FoundryProviderName, strings.Join(config.foundryLayers, ", ")),
			"use microsoft.foundry for only one infrastructure layer",
		)
	}
	return nil
}

type foundryInfraConfig struct {
	rootProvider  string
	foundryLayers []string
	hasLayers     bool
}

func (c foundryInfraConfig) hasFoundryLayer(name string) bool {
	return name != "" && slices.Contains(c.foundryLayers, name)
}

func parseFoundryInfraConfig(rawYAML []byte) (foundryInfraConfig, error) {
	var doc struct {
		Infra struct {
			Provider string `yaml:"provider"`
			Layers   []struct {
				Name     string `yaml:"name"`
				Provider string `yaml:"provider"`
			} `yaml:"layers"`
		} `yaml:"infra"`
	}
	if err := yaml.Unmarshal(rawYAML, &doc); err != nil {
		return foundryInfraConfig{}, err
	}

	config := foundryInfraConfig{
		rootProvider: strings.TrimSpace(doc.Infra.Provider),
		hasLayers:    len(doc.Infra.Layers) > 0,
	}
	config.foundryLayers = make([]string, 0, len(doc.Infra.Layers))
	for _, layer := range doc.Infra.Layers {
		provider := strings.TrimSpace(layer.Provider)
		if provider == "" {
			provider = config.rootProvider
		}
		if provider == FoundryProviderName {
			config.foundryLayers = append(config.foundryLayers, layer.Name)
		}
	}
	return config, nil
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
