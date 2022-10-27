// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azcli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"regexp"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerregistry/armcontainerregistry"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	azdinternal "github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/telemetry"
	"github.com/azure/azure-dev/cli/azd/pkg/azsdk"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
	"github.com/azure/azure-dev/cli/azd/pkg/identity"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/internal"
	"github.com/blang/semver/v4"
)

var (
	ErrAzCliNotLoggedIn         = errors.New("cli is not logged in. Try running \"azd login\" to fix")
	ErrAzCliRefreshTokenExpired = errors.New("refresh token has expired. Try running \"azd login\" to fix")
	ErrClientAssertionExpired   = errors.New("client assertion expired")
	ErrDeploymentNotFound       = errors.New("deployment not found")
	ErrNoConfigurationValue     = errors.New("no value configured")
	ErrAzCliSecretNotFound      = errors.New("secret not found")
)

const (
	// CollectTelemetryEnvVarName is the name of the variable that the Azure CLI uses to disable telemetry
	// when you're not using persistent configuration via `az config`
	collectTelemetryEnvVarName = "AZURE_CORE_COLLECT_TELEMETRY"
)

type AzCli interface {
	tools.ExternalTool

	// SetUserAgent sets the user agent that's sent with each call to the Azure
	// CLI via the `AZURE_HTTP_USER_AGENT` environment variable.
	SetUserAgent(userAgent string)

	// UserAgent gets the currently configured user agent
	UserAgent() string

	// Login runs the `az login` flow.  When `useDeviceCode` is true, a device code based login is preformed, otherwise
	// the interactive browser login flow happens. In the case of a device code login, the message is written to the
	// `deviceCodeWriter`.
	Login(ctx context.Context, useDeviceCode bool, deviceCodeWriter io.Writer) error
	LoginAcr(ctx context.Context, subscriptionId string, loginServer string) error
	GetContainerRegistries(ctx context.Context, subscriptionId string) ([]*armcontainerregistry.Registry, error)
	ListAccounts(ctx context.Context) ([]*AzCliSubscriptionInfo, error)
	GetDefaultAccount(ctx context.Context) (*AzCliSubscriptionInfo, error)
	GetAccount(ctx context.Context, subscriptionId string) (*AzCliSubscriptionInfo, error)
	GetCliConfigValue(ctx context.Context, name string) (AzCliConfigValue, error)
	GetSubscriptionDeployment(
		ctx context.Context,
		subscriptionId string,
		deploymentName string,
	) (*armresources.DeploymentExtended, error)
	GetResourceGroupDeployment(
		ctx context.Context,
		subscriptionId string,
		resourceGroupName string,
		deploymentName string,
	) (*armresources.DeploymentExtended, error)
	GetResource(ctx context.Context, subscriptionId string, resourceId string) (AzCliResourceExtended, error)
	GetKeyVault(
		ctx context.Context,
		subscriptionId string,
		resourceGroupName string,
		vaultName string,
	) (*AzCliKeyVault, error)
	GetKeyVaultSecret(ctx context.Context, vaultName string, secretName string) (*AzCliKeyVaultSecret, error)
	PurgeKeyVault(ctx context.Context, subscriptionId string, vaultName string, location string) error
	GetAppConfig(
		ctx context.Context, subscriptionId string, resourceGroupName string, configName string) (*AzCliAppConfig, error)
	PurgeAppConfig(ctx context.Context, subscriptionId string, configName string, location string) error
	DeployAppServiceZip(
		ctx context.Context,
		subscriptionId string,
		resourceGroup string,
		appName string,
		deployZipFile io.Reader,
	) (*string, error)
	DeployFunctionAppUsingZipFile(
		ctx context.Context,
		subscriptionID string,
		resourceGroup string,
		funcName string,
		deployZipFile io.Reader,
	) (*string, error)
	GetFunctionAppProperties(
		ctx context.Context,
		subscriptionID string,
		resourceGroup string,
		funcName string,
	) (*AzCliFunctionAppProperties, error)
	DeployToSubscription(
		ctx context.Context, subscriptionId, deploymentName string,
		armTemplate *azure.ArmTemplate,
		parametersPath, location string) (
		AzCliDeploymentResult, error)
	DeployToResourceGroup(
		ctx context.Context,
		subscriptionId,
		resourceGroup,
		deploymentName string,
		armTemplate *azure.ArmTemplate,
		parametersPath string,
	) (AzCliDeploymentResult, error)
	DeleteSubscriptionDeployment(ctx context.Context, subscriptionId string, deploymentName string) error
	DeleteResourceGroup(ctx context.Context, subscriptionId string, resourceGroupName string) error
	ListResourceGroup(
		ctx context.Context,
		subscriptionId string,
		listOptions *ListResourceGroupOptions,
	) ([]AzCliResource, error)
	ListResourceGroupResources(
		ctx context.Context,
		subscriptionId string,
		resourceGroupName string,
		listOptions *ListResourceGroupResourcesOptions,
	) ([]AzCliResource, error)
	ListSubscriptionDeploymentOperations(
		ctx context.Context,
		subscriptionId string,
		deploymentName string,
	) ([]*armresources.DeploymentOperation, error)
	ListResourceGroupDeploymentOperations(
		ctx context.Context,
		subscriptionId string,
		resourceGroupName string,
		deploymentName string,
	) ([]*armresources.DeploymentOperation, error)
	// ListAccountLocations lists the physical locations in Azure.
	ListAccountLocations(ctx context.Context, subscriptionId string) ([]AzCliLocation, error)
	// CreateOrUpdateServicePrincipal creates a service principal using a given name and returns a JSON object which
	// may be used by tools which understand the `AZURE_CREDENTIALS` format (i.e. the `sdk-auth` format). The service
	// principal is assigned a given role. If an existing principal exists with the given name,
	// it is updated in place and its credentials are reset.
	CreateOrUpdateServicePrincipal(
		ctx context.Context,
		subscriptionId string,
		applicationName string,
		roleToAssign string,
	) (json.RawMessage, error)
	GetAppServiceProperties(
		ctx context.Context,
		subscriptionId string,
		resourceGroupName string,
		applicationName string,
	) (*AzCliAppServiceProperties, error)
	GetContainerAppProperties(
		ctx context.Context,
		subscriptionId string,
		resourceGroupName string,
		applicationName string,
	) (*AzCliContainerAppProperties, error)
	GetStaticWebAppProperties(
		ctx context.Context,
		subscriptionID string,
		resourceGroup string,
		appName string,
	) (*AzCliStaticWebAppProperties, error)
	GetStaticWebAppApiKey(ctx context.Context, subscriptionID string, resourceGroup string, appName string) (*string, error)
	GetStaticWebAppEnvironmentProperties(
		ctx context.Context,
		subscriptionID string,
		resourceGroup string,
		appName string,
		environmentName string,
	) (*AzCliStaticWebAppEnvironmentProperties, error)

	GetSignedInUserId(ctx context.Context) (*string, error)

	GetAccessToken(ctx context.Context) (*AzCliAccessToken, error)
}

type AzCliDeployment struct {
	Id         string                    `json:"id"`
	Name       string                    `json:"name"`
	Properties AzCliDeploymentProperties `json:"properties"`
}

type AzCliDeploymentProperties struct {
	CorrelationId   string                                `json:"correlationId"`
	Error           AzCliDeploymentErrorResponse          `json:"error"`
	Dependencies    []AzCliDeploymentPropertiesDependency `json:"dependencies"`
	OutputResources []AzCliDeploymentResourceReference    `json:"outputResources"`
	Outputs         map[string]AzCliDeploymentOutput      `json:"outputs"`
}

type AzCliDeploymentPropertiesDependency struct {
	AzCliDeploymentPropertiesBasicDependency
	DependsOn []AzCliDeploymentPropertiesBasicDependency `json:"dependsOn"`
}

type AzCliDeploymentPropertiesBasicDependency struct {
	Id           string `json:"id"`
	ResourceName string `json:"resourceName"`
	ResourceType string `json:"resourceType"`
}

type AzCliDeploymentResult struct {
	Properties AzCliDeploymentResultProperties `json:"properties"`
}

type AzCliDeploymentResultProperties struct {
	Outputs map[string]AzCliDeploymentOutput `json:"outputs"`
}

type AzCliDeploymentErrorResponse struct {
	Code           string                         `json:"code"`
	Message        string                         `json:"message"`
	Target         string                         `json:"target"`
	Details        []AzCliDeploymentErrorResponse `json:"details"`
	AdditionalInfo AzCliDeploymentAdditionalInfo  `json:"additionalInfo"`
}

type AzCliDeploymentAdditionalInfo struct {
	Type string      `json:"type"`
	Info interface{} `json:"info"`
}

type AzCliDeploymentOutput struct {
	Type  string      `json:"type"`
	Value interface{} `json:"value"`
}

type AzCliResource struct {
	Id       string `json:"id"`
	Name     string `json:"name"`
	Type     string `json:"type"`
	Location string `json:"location"`
}

type AzCliResourceExtended struct {
	AzCliResource
	Kind string `json:"kind"`
}

type AzCliDeploymentResourceReference struct {
	Id string `json:"id"`
}

type AzCliResourceOperation struct {
	Id          string                           `json:"id"`
	OperationId string                           `json:"operationId"`
	Properties  AzCliResourceOperationProperties `json:"properties"`
}

type AzCliResourceOperationProperties struct {
	ProvisioningOperation string                               `json:"provisioningOperation"`
	ProvisioningState     string                               `json:"provisioningState"`
	TargetResource        AzCliResourceOperationTargetResource `json:"targetResource"`
	StatusCode            string                               `json:"statusCode"`
	StatusMessage         AzCliDeploymentStatusMessage         `json:"statusMessage"`
	// While the operation is in progress, this timestamp effectively represents "InProgressTimestamp".
	// When the operation ends, this timestamp effectively represents "EndTimestamp".
	Timestamp time.Time `json:"timestamp"`
}

type AzCliDeploymentStatusMessage struct {
	Err    AzCliDeploymentErrorResponse `json:"error"`
	Status string                       `json:"status"`
}

type AzCliResourceOperationTargetResource struct {
	Id            string `json:"id"`
	ResourceType  string `json:"resourceType"`
	ResourceName  string `json:"resourceName"`
	ResourceGroup string `json:"resourceGroup"`
}

// AzCliConfigValue represents the value returned by `az config get`.
type AzCliConfigValue struct {
	Name   string `json:"name"`
	Source string `json:"source"`
	Value  string `json:"value"`
}

// AzCliConfigValue represents the value in the array returned by `az extension list`.
type AzCliExtensionInfo struct {
	Name string
}

// Optional parameters for resource group listing.
type ListResourceGroupOptions struct {
	// An optional tag filter
	TagFilter *Filter
	// An optional filter expression to filter the resource group results
	// https://learn.microsoft.com/en-us/rest/api/resources/resource-groups/list
	Filter *string
}

// Optional parameters for resource group resources listing.
type ListResourceGroupResourcesOptions struct {
	// An optional filter expression to filter the resource list result
	// https://learn.microsoft.com/en-us/rest/api/resources/resources/list-by-resource-group#uri-parameters
	Filter *string
}

type Filter struct {
	Key   string
	Value string
}

type NewAzCliArgs struct {
	EnableDebug     bool
	EnableTelemetry bool
	// CommandRunner allows us to stub out the command execution for testing
	CommandRunner exec.CommandRunner
	HttpClient    httputil.HttpClient
}

func NewAzCli(credential azcore.TokenCredential, args NewAzCliArgs) AzCli {
	if args.CommandRunner == nil {
		panic("NewAzCli: must set args.CommandRunner")
	}
	return &azCli{
		userAgent:       azdinternal.MakeUserAgentString(""),
		enableDebug:     args.EnableDebug,
		enableTelemetry: args.EnableTelemetry,
		commandRunner:   args.CommandRunner,
		httpClient:      args.HttpClient,
		credential:      credential,
	}
}

type azCli struct {
	userAgent       string
	enableDebug     bool
	enableTelemetry bool

	// commandRunner allows us to stub out the exec.CommandRunner, for testing.
	commandRunner exec.CommandRunner

	// Allows us to mock the Http Requests from the go modules
	httpClient httputil.HttpClient

	credential azcore.TokenCredential
}

func (cli *azCli) Name() string {
	return "Azure CLI"
}

func (cli *azCli) InstallUrl() string {
	return "https://aka.ms/azure-dev/azure-cli-install"
}

func (cli *azCli) versionInfo() tools.VersionInfo {
	return tools.VersionInfo{
		MinimumVersion: semver.Version{
			Major: 2,
			Minor: 38,
			Patch: 0},
		UpdateCommand: "Run \"az upgrade\" to upgrade",
	}
}

func (cli *azCli) unmarshalCliVersion(ctx context.Context, component string) (string, error) {
	azRes, err := tools.ExecuteCommand(ctx, "az", "version", "--output", "json")
	if err != nil {
		return "", err
	}
	var azVerMap map[string]interface{}
	err = json.Unmarshal([]byte(azRes), &azVerMap)
	if err != nil {
		return "", err
	}
	version, ok := azVerMap[component].(string)
	if !ok {
		return "", fmt.Errorf("reading %s component '%s' version failed", cli.Name(), component)
	}
	return version, nil
}

func (cli *azCli) CheckInstalled(ctx context.Context) (bool, error) {
	found, err := tools.ToolInPath("az")
	if !found {
		return false, err
	}
	azVer, err := cli.unmarshalCliVersion(ctx, "azure-cli")
	if err != nil {
		return false, fmt.Errorf("checking %s version:  %w", cli.Name(), err)
	}
	azSemver, err := semver.Parse(azVer)
	if err != nil {
		return false, fmt.Errorf("converting to semver version fails: %w", err)
	}
	updateDetail := cli.versionInfo()
	if azSemver.LT(updateDetail.MinimumVersion) {
		return false, &tools.ErrSemver{ToolName: cli.Name(), VersionInfo: updateDetail}
	}
	return true, nil
}

// SetUserAgent sets the user agent that's sent with each call to the Azure
// CLI via the `AZURE_HTTP_USER_AGENT` environment variable.
func (cli *azCli) SetUserAgent(userAgent string) {
	cli.userAgent = userAgent
}

func (cli *azCli) UserAgent() string {
	return cli.userAgent
}

func (cli *azCli) Login(ctx context.Context, useDeviceCode bool, deviceCodeWriter io.Writer) error {
	args := []string{"login", "--output", "none"}

	var writer io.Writer
	if useDeviceCode {
		writer = deviceCodeWriter
		args = append(args, "--use-device-code")
	}

	res, err := cli.runAzCommandWithArgs(ctx, exec.RunArgs{
		Args:   args,
		Stderr: writer,
	})

	if err != nil {
		return fmt.Errorf("failed running az login: %s: %w", res.String(), err)
	}

	return nil
}

func (cli *azCli) GetCliConfigValue(ctx context.Context, name string) (AzCliConfigValue, error) {
	res, err := cli.runAzCommand(ctx, "config", "get", name, "--output", "json")
	if isConfigurationIsNotSetMessage(res.Stderr) {
		return AzCliConfigValue{}, ErrNoConfigurationValue
	} else if err != nil {
		return AzCliConfigValue{}, fmt.Errorf("failed running config get: %s: %w", res.String(), err)
	}

	var value AzCliConfigValue
	if err := json.Unmarshal([]byte(res.Stdout), &value); err != nil {
		return AzCliConfigValue{}, fmt.Errorf("could not unmarshal output %s as an AzCliConfigValue: %w", res.Stdout, err)
	}

	return value, nil
}

func extractDeploymentError(stderr string) error {
	if start, end := findDeploymentErrorJsonIndex(stderr); start != -1 && end != -1 {
		deploymentError := internal.NewAzureDeploymentError(stderr[start:end])
		var innerErrorDetails string
		if len(stderr) >= end+1 {
			innerErrorDetails = extractInnerDeploymentErrors(stderr[end+1:])
		}

		return fmt.Errorf(
			"%s\n%w%s",
			output.WithErrorFormat("Deployment Error Details:"),
			deploymentError,
			innerErrorDetails,
		)
	}

	return nil
}

func extractInnerDeploymentErrors(stderr string) string {
	innerErrors := getInnerDeploymentErrorsJson(stderr)

	if len(innerErrors) == 0 {
		// Return raw text to be displayed
		return stderr
	} else {
		var sb strings.Builder
		for _, innerErrorJson := range innerErrors {
			innerError := internal.NewAzureDeploymentError(innerErrorJson)
			sb.WriteString(output.WithErrorFormat(fmt.Sprintf("\nInner Error:\n%s", innerError.Error())))
		}
		return sb.String()
	}
}

func (cli *azCli) runAzCommand(ctx context.Context, args ...string) (exec.RunResult, error) {
	return cli.runAzCommandWithArgs(ctx, exec.RunArgs{
		Args: args,
	})
}

// runAzCommandWithArgs will run the 'args', ignoring 'Cmd' in favor of injecting the proper
// 'az' alias.
func (cli *azCli) runAzCommandWithArgs(ctx context.Context, args exec.RunArgs) (exec.RunResult, error) {
	if cli.enableDebug {
		args.Args = append(args.Args, "--debug")
	}

	args.Cmd = "az"
	args.Env = append(args.Env, fmt.Sprintf("AZURE_HTTP_USER_AGENT=%s", cli.userAgent))

	if !cli.enableTelemetry {
		args.Env = append(args.Env, fmt.Sprintf("%s=no", collectTelemetryEnvVarName))
	}

	args.Debug = cli.enableDebug

	return cli.commandRunner.Run(ctx, args)
}

func (cli *azCli) createDefaultClientOptionsBuilder(ctx context.Context) *azsdk.ClientOptionsBuilder {
	return azsdk.NewClientOptionsBuilder().
		WithTransport(httputil.GetHttpClient(ctx)).
		WithPerCallPolicy(azsdk.NewUserAgentPolicy(cli.UserAgent()))
}

// Azure Active Directory codes can be referenced via https://login.microsoftonline.com/error?code=<ERROR_CODE>,
// where ERROR_CODE is the digits portion of an AAD error code. Example: AADSTS70043 has error code 70043
// Additionally, https://learn.microsoft.com/azure/active-directory/develop/reference-aadsts-error-codes#aadsts-error-codes
// is a helpful resource with a list of error codes and messages.

var isConfigurationIsNotSetMessageRegex = regexp.MustCompile(`Configuration '.*' is not set\.`)
var isDeploymentErrorRegex = regexp.MustCompile(`ERROR: ({.+})`)
var isInnerDeploymentErrorRegex = regexp.MustCompile(`Inner Errors:\s+({.+})`)

func isConfigurationIsNotSetMessage(s string) bool {
	return isConfigurationIsNotSetMessageRegex.MatchString(s)
}

func findDeploymentErrorJsonIndex(s string) (int, int) {
	index := isDeploymentErrorRegex.FindStringSubmatchIndex(s)

	if index == nil {
		return -1, -1
	} else if len(index) >= 4 { // [matchStart, matchEnd, submatchStart, submatchEnd]
		return index[2], index[3]
	}

	return -1, -1
}

func getInnerDeploymentErrorsJson(s string) []string {
	results := []string{}
	matches := isInnerDeploymentErrorRegex.FindAllStringSubmatch(s, -1)
	if matches == nil {
		return results
	}

	for _, match := range matches {
		if len(match) > 1 {
			results = append(results, match[1])
		}
	}

	return results
}

type contextKey string

const (
	azCliContextKey contextKey = "azcli"
)

func WithAzCli(ctx context.Context, azCli AzCli) context.Context {
	return context.WithValue(ctx, azCliContextKey, azCli)
}

func GetAzCli(ctx context.Context) AzCli {
	// Check to see if we already have an az cli in the context
	azCli, ok := ctx.Value(azCliContextKey).(AzCli)
	if !ok {
		options := azdinternal.GetCommandOptions(ctx)
		credential := identity.GetCredentials(ctx)

		commandRunner := exec.GetCommandRunner(ctx)
		args := NewAzCliArgs{
			EnableDebug:     options.EnableDebugLogging,
			EnableTelemetry: options.EnableTelemetry,
			CommandRunner:   commandRunner,
		}
		azCli = NewAzCli(credential, args)
	}

	// Set the user agent if a template has been selected
	template := telemetry.TemplateFromContext(ctx)
	userAgent := azdinternal.MakeUserAgentString(template)
	azCli.SetUserAgent(userAgent)

	return azCli
}
