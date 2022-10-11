// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerregistry/armcontainerregistry"
	azdinternal "github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/internal/telemetry"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/internal"
	"github.com/blang/semver/v4"
)

var (
	ErrAzCliNotLoggedIn          = errors.New("cli is not logged in. Try running \"azd login\" to fix")
	ErrAzCliRefreshTokenExpired  = errors.New("refresh token has expired. Try running \"azd login\" to fix")
	ErrCurrentPrincipalIsNotUser = errors.New("current principal is not a user principal")
	ErrClientAssertionExpired    = errors.New("client assertion expired")
	ErrDeploymentNotFound        = errors.New("deployment not found")
	ErrNoConfigurationValue      = errors.New("no value configured")
	ErrAzCliSecretNotFound       = errors.New("secret not found")
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
	ListAccounts(ctx context.Context) ([]AzCliSubscriptionInfo, error)
	GetDefaultAccount(ctx context.Context) (*AzCliSubscriptionInfo, error)
	GetAccount(ctx context.Context, subscriptionId string) (*AzCliSubscriptionInfo, error)
	ListExtensions(ctx context.Context) ([]AzCliExtensionInfo, error)
	GetCliConfigValue(ctx context.Context, name string) (AzCliConfigValue, error)
	GetSubscriptionTenant(ctx context.Context, subscriptionId string) (string, error)
	GetSubscriptionDeployment(ctx context.Context, subscriptionId string, deploymentName string) (AzCliDeployment, error)
	GetResourceGroupDeployment(ctx context.Context, subscriptionId string, resourceGroupName string, deploymentName string) (AzCliDeployment, error)
	GetResource(ctx context.Context, subscriptionId string, resourceId string) (AzCliResourceExtended, error)
	GetKeyVault(ctx context.Context, subscriptionId string, resourceGroupName string, vaultName string) (*AzCliKeyVault, error)
	GetKeyVaultSecret(ctx context.Context, vaultName string, secretName string) (*AzCliKeyVaultSecret, error)
	PurgeKeyVault(ctx context.Context, subscriptionId string, vaultName string, location string) error
	DeployAppServiceZip(ctx context.Context, subscriptionId string, resourceGroup string, appName string, deployZipPath string) (string, error)
	DeployFunctionAppUsingZipFile(ctx context.Context, subscriptionID string, resourceGroup string, funcName string, deployZipPath string) (string, error)
	GetFunctionAppProperties(ctx context.Context, subscriptionID string, resourceGroup string, funcName string) (AzCliFunctionAppProperties, error)
	DeployToSubscription(ctx context.Context, subscriptionId string, deploymentName string, templatePath string, parametersPath string, location string) (AzCliDeploymentResult, error)
	DeployToResourceGroup(ctx context.Context, subscriptionId string, resourceGroup string, deploymentName string, templatePath string, parametersPath string) (AzCliDeploymentResult, error)
	DeleteSubscriptionDeployment(ctx context.Context, subscriptionId string, deploymentName string) error
	DeleteResourceGroup(ctx context.Context, subscriptionId string, resourceGroupName string) error
	ListResourceGroup(ctx context.Context, subscriptionId string, listOptions *ListResourceGroupOptions) ([]AzCliResource, error)
	ListResourceGroupResources(ctx context.Context, subscriptionId string, resourceGroupName string, listOptions *ListResourceGroupResourcesOptions) ([]AzCliResource, error)
	ListSubscriptionDeploymentOperations(ctx context.Context, subscriptionId string, deploymentName string) ([]AzCliResourceOperation, error)
	ListResourceGroupDeploymentOperations(ctx context.Context, subscriptionId string, resourceGroupName string, deploymentName string) ([]AzCliResourceOperation, error)
	// ListAccountLocations lists the physical locations in Azure.
	ListAccountLocations(ctx context.Context, subscriptionId string) ([]AzCliLocation, error)
	// CreateOrUpdateServicePrincipal creates a service principal using a given name and returns a JSON object which
	// may be used by tools which understand the `AZURE_CREDENTIALS` format (i.e. the `sdk-auth` format). The service
	// principal is assigned a given role. If an existing principal exists with the given name,
	// it is updated in place and its credentials are reset.
	CreateOrUpdateServicePrincipal(ctx context.Context, subscriptionId string, applicationName string, roleToAssign string) (json.RawMessage, error)
	GetAppServiceProperties(ctx context.Context, subscriptionId string, resourceGroupName string, applicationName string) (AzCliAppServiceProperties, error)
	GetContainerAppProperties(ctx context.Context, subscriptionId string, resourceGroupName string, applicationName string) (AzCliContainerAppProperties, error)
	GetStaticWebAppProperties(ctx context.Context, subscriptionID string, resourceGroup string, appName string) (AzCliStaticWebAppProperties, error)
	GetStaticWebAppApiKey(ctx context.Context, subscriptionID string, resourceGroup string, appName string) (string, error)
	GetStaticWebAppEnvironmentProperties(ctx context.Context, subscriptionID string, resourceGroup string, appName string, environmentName string) (AzCliStaticWebAppEnvironmentProperties, error)

	GetSignedInUserId(ctx context.Context) (string, error)

	GetAccessToken(ctx context.Context) (AzCliAccessToken, error)

	// GraphQuery performs a query against Azure Resource Graph.
	//
	// This allows free-form querying of resources by any attribute, which is powerful.
	// However, results may be delayed for multiple minutes. Ensure that your this fits your use-case.
	GraphQuery(ctx context.Context, query string, subscriptions []string) (*AzCliGraphQuery, error)
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

type AzCliAppServiceProperties struct {
	HostNames []string `json:"hostNames"`
}

type AzCliContainerAppProperties struct {
	Properties struct {
		Configuration struct {
			Ingress struct {
				Fqdn string `json:"fqdn"`
			} `json:"ingress"`
		} `json:"configuration"`
	} `json:"properties"`
}

type AzCliFunctionAppProperties struct {
	HostNames []string `json:"hostNames"`
}

type AzCliStaticWebAppProperties struct {
	DefaultHostname string `json:"defaultHostname"`
}

type AzCliStaticWebAppEnvironmentProperties struct {
	Hostname string `json:"hostname"`
	Status   string `json:"status"`
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

// AzCliAccessToken represents the value returned by `az account get-access-token`
type AzCliAccessToken struct {
	AccessToken string
	ExpiresOn   *time.Time
}

type AzCliGraphQuery struct {
	Count        int             `json:"count"`
	Data         []AzCliResource `json:"data"`
	SkipToken    string          `json:"skipToken"`
	TotalRecords int             `json:"totalRecords"`
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

func (tok *AzCliAccessToken) UnmarshalJSON(data []byte) error {
	var wire struct {
		AccessToken string `json:"accessToken"`
		ExpiresOn   string `json:"expiresOn"`
	}

	if err := json.Unmarshal(data, &wire); err != nil {
		return fmt.Errorf("unmarshalling json: %w", err)
	}

	tok.AccessToken = wire.AccessToken

	// the format of the ExpiresOn property of the access token differs across environments
	// see https://github.com/Azure/azure-sdk-for-go/blob/61e2e74b9af2cfbff74ea8bb3c6f687c582c419f/sdk/azidentity/azure_cli_credential.go
	//
	// nolint:errorlint
	parseExpirationDate := func(input string) (*time.Time, error) {
		// CloudShell (and potentially the Azure CLI in future)
		expirationDate, cloudShellErr := time.Parse(time.RFC3339, input)
		if cloudShellErr != nil {
			// Azure CLI (Python) e.g. 2017-08-31 19:48:57.998857 (plus the local timezone)
			const cliFormat = "2006-01-02 15:04:05.999999"
			expirationDate, cliErr := time.ParseInLocation(cliFormat, input, time.Local)
			if cliErr != nil {
				return nil, fmt.Errorf("Error parsing expiration date %q.\n\nCloudShell Error: \n%+v\n\nCLI Error:\n%+v", input, cloudShellErr, cliErr)
			}
			return &expirationDate, nil
		}
		return &expirationDate, nil
	}

	expiresOn, err := parseExpirationDate(wire.ExpiresOn)
	if err != nil {
		return fmt.Errorf("parsing expiresOn: %w", err)
	}

	tok.ExpiresOn = expiresOn
	return nil
}

type NewAzCliArgs struct {
	EnableDebug     bool
	EnableTelemetry bool
	// CommandRunner allows us to stub out the command execution for testing
	CommandRunner exec.CommandRunner
	HttpClient    httputil.HttpClient
}

func NewAzCli(args NewAzCliArgs) AzCli {
	if args.CommandRunner == nil {
		args.CommandRunner = exec.NewCommandRunner()
	}

	return &azCli{
		userAgent:       azdinternal.MakeUserAgentString(""),
		enableDebug:     args.EnableDebug,
		enableTelemetry: args.EnableTelemetry,
		commandRunner:   args.CommandRunner,
		httpClient:      args.HttpClient,
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

func (cli *azCli) ListExtensions(ctx context.Context) ([]AzCliExtensionInfo, error) {
	res, err := cli.runAzCommand(ctx, "extension", "list", "--output", "json")

	if err != nil {
		return nil, fmt.Errorf("failed running az extension list: %s: %w", res.String(), err)
	}

	var extensionInfo []AzCliExtensionInfo
	if err := json.Unmarshal([]byte(res.Stdout), &extensionInfo); err != nil {
		return nil, fmt.Errorf("could not unmarshal output %s as a []AzCliExtensionInfo: %w", res.Stdout, err)
	}
	return extensionInfo, nil
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

func (cli *azCli) DeployAppServiceZip(ctx context.Context, subscriptionId string, resourceGroup string, appName string, deployZipPath string) (string, error) {
	res, err := cli.runAzCommand(ctx, "webapp", "deployment", "source", "config-zip", "--subscription", subscriptionId, "--resource-group", resourceGroup, "--name", appName, "--src", deployZipPath, "--timeout", "3600", "--output", "json")
	if isNotLoggedInMessage(res.Stderr) {
		return "", ErrAzCliNotLoggedIn
	} else if err != nil {
		return "", fmt.Errorf("failed running az deployment source config-zip: %s: %w", res.String(), err)
	}

	return res.Stdout, nil
}

func (cli *azCli) DeployFunctionAppUsingZipFile(ctx context.Context, subscriptionID string, resourceGroup string, funcName string, deployZipPath string) (string, error) {
	// eg: az functionapp deployment source config-zip -g <resource_group> -n <app_name> --src <zip_file_path>
	res, err := cli.runAzCommandWithArgs(ctx, exec.RunArgs{
		Args: []string{
			"functionapp", "deployment", "source", "config-zip",
			"--subscription", subscriptionID,
			"--resource-group", resourceGroup,
			"--name", funcName,
			"--src", deployZipPath,
			"--build-remote", "true",
			"--timeout", "3600",
		},
		EnrichError: true,
	})

	if err != nil {
		return "", fmt.Errorf("failed deploying function app: %w", err)
	}

	return res.Stdout, nil
}

func (cli *azCli) GetAppServiceProperties(ctx context.Context, subscriptionId string, resourceGroup string, appName string) (AzCliAppServiceProperties, error) {
	res, err := cli.runAzCommand(ctx, "webapp", "show", "--subscription", subscriptionId, "--resource-group", resourceGroup, "--name", appName, "--output", "json")
	if isNotLoggedInMessage(res.Stderr) {
		return AzCliAppServiceProperties{}, ErrAzCliNotLoggedIn
	} else if err != nil {
		return AzCliAppServiceProperties{}, fmt.Errorf("failed running az webapp show: %s: %w", res.String(), err)
	}

	var appServiceProperties AzCliAppServiceProperties
	if err := json.Unmarshal([]byte(res.Stdout), &appServiceProperties); err != nil {
		return AzCliAppServiceProperties{}, fmt.Errorf("could not unmarshal output %s as an AzCliAppServiceProperties: %w", res.Stdout, err)
	}

	return appServiceProperties, nil
}

func (cli *azCli) GetContainerAppProperties(ctx context.Context, subscriptionId, resourceGroup, appName string) (AzCliContainerAppProperties, error) {
	res, err := cli.runAzCommand(ctx, "resource", "show", "--subscription", subscriptionId, "--resource-group", resourceGroup, "--name", appName, "--resource-type", "Microsoft.App/containerApps", "--output", "json")
	if isNotLoggedInMessage(res.Stderr) {
		return AzCliContainerAppProperties{}, ErrAzCliNotLoggedIn
	} else if err != nil {
		return AzCliContainerAppProperties{}, fmt.Errorf("failed running az resource show: %s: %w", res.String(), err)
	}

	var containerAppProperties AzCliContainerAppProperties
	if err := json.Unmarshal([]byte(res.Stdout), &containerAppProperties); err != nil {
		return AzCliContainerAppProperties{}, fmt.Errorf("could not unmarshal output %s as an AzCliContainerAppProperties: %w", res.Stdout, err)
	}

	return containerAppProperties, nil
}

func (cli *azCli) GetFunctionAppProperties(ctx context.Context, subscriptionID string, resourceGroup string, funcName string) (AzCliFunctionAppProperties, error) {
	res, err := cli.runAzCommandWithArgs(ctx, exec.RunArgs{
		Args: []string{
			"functionapp", "show",
			"--subscription", subscriptionID,
			"--resource-group", resourceGroup,
			"--name", funcName,
			"--output", "json",
		},
		EnrichError: true,
	})

	if err != nil {
		return AzCliFunctionAppProperties{}, fmt.Errorf("failed getting functionapp properties: %w", err)
	}

	var funcAppProperties AzCliFunctionAppProperties
	if err := json.Unmarshal([]byte(res.Stdout), &funcAppProperties); err != nil {
		return AzCliFunctionAppProperties{}, fmt.Errorf("could not unmarshal output %s as an AzCliFunctionAppProperties: %w", res.Stdout, err)
	}

	return funcAppProperties, nil
}

func (cli *azCli) GetStaticWebAppProperties(ctx context.Context, subscriptionID string, resourceGroup string, appName string) (AzCliStaticWebAppProperties, error) {
	res, err := cli.runAzCommandWithArgs(ctx, exec.RunArgs{
		Args: []string{
			"staticwebapp", "show",
			"--subscription", subscriptionID,
			"--resource-group", resourceGroup,
			"--name", appName,
			"--output", "json",
		},
		EnrichError: true,
	})

	if err != nil {
		return AzCliStaticWebAppProperties{}, fmt.Errorf("failed getting staticwebapp properties: %w", err)
	}

	var staticWebAppProperties AzCliStaticWebAppProperties
	if err := json.Unmarshal([]byte(res.Stdout), &staticWebAppProperties); err != nil {
		return AzCliStaticWebAppProperties{}, fmt.Errorf("could not unmarshal output %s as an AzCliStaticWebAppProperties: %w", res.Stdout, err)
	}

	return staticWebAppProperties, nil
}

func (cli *azCli) GetStaticWebAppEnvironmentProperties(ctx context.Context, subscriptionID string, resourceGroup string, appName string, environmentName string) (AzCliStaticWebAppEnvironmentProperties, error) {
	res, err := cli.runAzCommandWithArgs(ctx, exec.RunArgs{
		Args: []string{
			"staticwebapp", "environment", "show",
			"--subscription", subscriptionID,
			"--resource-group", resourceGroup,
			"--name", appName,
			"--environment", environmentName,
			"--output", "json",
		},
		EnrichError: true,
	})

	if err != nil {
		return AzCliStaticWebAppEnvironmentProperties{}, fmt.Errorf("failed getting staticwebapp environment properties: %w", err)
	}

	var environmentProperties AzCliStaticWebAppEnvironmentProperties
	if err := json.Unmarshal([]byte(res.Stdout), &environmentProperties); err != nil {
		return AzCliStaticWebAppEnvironmentProperties{}, fmt.Errorf("could not unmarshal output %s as an AzCliStaticWebAppEnvironmentProperties: %w", res.Stdout, err)
	}

	return environmentProperties, nil
}

func (cli *azCli) GetStaticWebAppApiKey(ctx context.Context, subscriptionID string, resourceGroup string, appName string) (string, error) {
	res, err := cli.runAzCommandWithArgs(ctx, exec.RunArgs{
		Args: []string{
			"staticwebapp", "secrets", "list",
			"--subscription", subscriptionID,
			"--resource-group", resourceGroup,
			"--name", appName,
			"--query", "properties.apiKey",
			"--output", "tsv",
		},
		EnrichError: true,
	})

	if err != nil {
		return "", fmt.Errorf("failed getting staticwebapp api key: %w", err)
	}

	return strings.TrimSpace(res.Stdout), nil
}

func extractDeploymentError(stderr string) error {
	if start, end := findDeploymentErrorJsonIndex(stderr); start != -1 && end != -1 {
		deploymentError := internal.NewAzureDeploymentError(stderr[start:end])
		var innerErrorDetails string
		if len(stderr) >= end+1 {
			innerErrorDetails = extractInnerDeploymentErrors(stderr[end+1:])
		}

		return fmt.Errorf("%s\n%w%s", output.WithErrorFormat("Deployment Error Details:"), deploymentError, innerErrorDetails)
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

func (cli *azCli) DeployToSubscription(ctx context.Context, subscriptionId string, deploymentName string, templateFile string, parametersFile string, location string) (AzCliDeploymentResult, error) {
	res, err := cli.runAzCommand(ctx, "deployment", "sub", "create", "--subscription", subscriptionId, "--name", deploymentName, "--location", location, "--template-file", templateFile, "--parameters", fmt.Sprintf("@%s", parametersFile), "--output", "json")
	if isNotLoggedInMessage(res.Stderr) {
		return AzCliDeploymentResult{}, ErrAzCliNotLoggedIn
	} else if err != nil {
		if deploymentError := extractDeploymentError(res.Stderr); deploymentError != nil {
			return AzCliDeploymentResult{}, fmt.Errorf("failed running az deployment sub create:\n\n%w", deploymentError)
		}

		return AzCliDeploymentResult{}, fmt.Errorf("failed running az deployment sub create: %s: %w", res.String(), err)
	}

	var deploymentResult AzCliDeploymentResult
	if err := json.Unmarshal([]byte(res.Stdout), &deploymentResult); err != nil {
		return AzCliDeploymentResult{}, fmt.Errorf("could not unmarshal output %s as an AzCliDeploymentResult: %w", res.Stdout, err)
	}
	return deploymentResult, nil
}

func (cli *azCli) DeployToResourceGroup(ctx context.Context, subscriptionId string, resourceGroup string, deploymentName string, templateFile string, parametersFile string) (AzCliDeploymentResult, error) {
	res, err := cli.runAzCommand(ctx, "deployment", "group", "create", "--subscription", subscriptionId, "--resource-group", resourceGroup, "--name", deploymentName, "--template-file", templateFile, "--parameters", fmt.Sprintf("@%s", parametersFile), "--output", "json")
	if isNotLoggedInMessage(res.Stderr) {
		return AzCliDeploymentResult{}, ErrAzCliNotLoggedIn
	} else if err != nil {
		if deploymentError := extractDeploymentError(res.Stderr); deploymentError != nil {
			return AzCliDeploymentResult{}, fmt.Errorf("failed running az deployment group create:\n\n%w", deploymentError)
		}

		return AzCliDeploymentResult{}, fmt.Errorf("failed running az deployment group create: %s: %w", res.String(), err)
	}

	var deploymentResult AzCliDeploymentResult
	if err := json.Unmarshal([]byte(res.Stdout), &deploymentResult); err != nil {
		return AzCliDeploymentResult{}, fmt.Errorf("could not unmarshal output %s as an AzCliDeploymentResult: %w", res.Stdout, err)
	}
	return deploymentResult, nil
}

func (cli *azCli) DeleteSubscriptionDeployment(ctx context.Context, subscriptionId string, deploymentName string) error {
	res, err := cli.runAzCommand(ctx, "deployment", "sub", "delete", "--subscription", subscriptionId, "--name", deploymentName, "--output", "json")
	if isNotLoggedInMessage(res.Stderr) {
		return ErrAzCliNotLoggedIn
	} else if err != nil {
		return fmt.Errorf("failed running az deployment sub delete: %s: %w", res.String(), err)
	}

	return nil
}

func (cli *azCli) ListSubscriptionDeploymentOperations(ctx context.Context, subscriptionId string, deploymentName string) ([]AzCliResourceOperation, error) {
	res, err := cli.runAzCommand(ctx, "deployment", "operation", "sub", "list", "--subscription", subscriptionId, "--name", deploymentName, "--output", "json")
	if isNotLoggedInMessage(res.Stderr) {
		return nil, ErrAzCliNotLoggedIn
	} else if isDeploymentNotFoundMessage(res.Stderr) {
		return nil, ErrDeploymentNotFound
	} else if err != nil {
		return nil, fmt.Errorf("failed running az deployment operation sub list: %s: %w", res.String(), err)
	}

	var resources []AzCliResourceOperation
	if err := json.Unmarshal([]byte(res.Stdout), &resources); err != nil {
		return nil, fmt.Errorf("could not unmarshal output %s as a []AzCliResourceOperation: %w", res.Stdout, err)
	}
	return resources, nil
}

func (cli *azCli) ListResourceGroupDeploymentOperations(ctx context.Context, subscriptionId string, resourceGroupName string, deploymentName string) ([]AzCliResourceOperation, error) {
	res, err := cli.runAzCommand(ctx, "deployment", "operation", "group", "list", "--subscription", subscriptionId, "--resource-group", resourceGroupName, "--name", deploymentName, "--output", "json")
	if isNotLoggedInMessage(res.Stderr) {
		return nil, ErrAzCliNotLoggedIn
	} else if isDeploymentNotFoundMessage(res.Stderr) {
		return nil, ErrDeploymentNotFound
	} else if err != nil {
		return nil, fmt.Errorf("failed running az deployment operation group list: %s: %w", res.String(), err)
	}

	var resources []AzCliResourceOperation
	if err := json.Unmarshal([]byte(res.Stdout), &resources); err != nil {
		return nil, fmt.Errorf("could not unmarshal output %s as a []AzCliResourceOperation: %w", res.Stdout, err)
	}
	return resources, nil
}

func (cli *azCli) GetSubscriptionDeployment(ctx context.Context, subscriptionId string, deploymentName string) (AzCliDeployment, error) {
	res, err := cli.runAzCommand(ctx, "deployment", "sub", "show", "--subscription", subscriptionId, "--name", deploymentName, "--output", "json")
	if isNotLoggedInMessage(res.Stderr) {
		return AzCliDeployment{}, ErrAzCliNotLoggedIn
	} else if isDeploymentNotFoundMessage(res.Stderr) {
		return AzCliDeployment{}, ErrDeploymentNotFound
	} else if err != nil {
		return AzCliDeployment{}, fmt.Errorf("failed running az deployment sub show: %s: %w", res.String(), err)
	}

	var deployment AzCliDeployment
	if err := json.Unmarshal([]byte(res.Stdout), &deployment); err != nil {
		return AzCliDeployment{}, fmt.Errorf("could not unmarshal output %s as an AzCliDeployment: %w", res.Stdout, err)
	}
	return deployment, nil
}

func (cli *azCli) GetResourceGroupDeployment(ctx context.Context, subscriptionId string, resourceGroupName string, deploymentName string) (AzCliDeployment, error) {
	res, err := cli.runAzCommand(ctx, "deployment", "group", "show", "--subscription", subscriptionId, "--resource-group", resourceGroupName, "--name", deploymentName, "--output", "json")
	if isNotLoggedInMessage(res.Stderr) {
		return AzCliDeployment{}, ErrAzCliNotLoggedIn
	} else if isDeploymentNotFoundMessage(res.Stderr) {
		return AzCliDeployment{}, ErrDeploymentNotFound
	} else if err != nil {
		return AzCliDeployment{}, fmt.Errorf("failed running az deployment sub show: %s: %w", res.String(), err)
	}

	var deployment AzCliDeployment
	if err := json.Unmarshal([]byte(res.Stdout), &deployment); err != nil {
		return AzCliDeployment{}, fmt.Errorf("could not unmarshal output %s as an AzCliDeployment: %w", res.Stdout, err)
	}
	return deployment, nil
}

func (cli *azCli) GetSignedInUserId(ctx context.Context) (string, error) {
	res, err := cli.runAzCommand(ctx, "ad", "signed-in-user", "show", "--query", "objectId", "--output", "json")
	if isNotLoggedInMessage(res.Stderr) {
		return "", ErrAzCliNotLoggedIn
	} else if isResourceSegmentMeNotFoundMessage(res.Stderr) {
		return "", ErrCurrentPrincipalIsNotUser
	} else if isClientAssertionInvalidMessage(res.Stderr) {
		return "", ErrClientAssertionExpired
	} else if err != nil {
		return "", fmt.Errorf("failed running az signed-in-user show: %s: %w", res.String(), err)
	}

	var objectId string
	if err := json.Unmarshal([]byte(res.Stdout), &objectId); err != nil {
		return "", fmt.Errorf("could not unmarshal output %s as a string: %w", res.Stdout, err)
	}
	return objectId, nil
}

// Default response model from `az ad sp`
type ServicePrincipalCredentials struct {
	AppId       string `json:"appId"`
	DisplayName string `json:"displayName"`
	Password    string `json:"password"`
	Tenant      string `json:"tenant"`
}

// Required model structure for Azure Credentials tools
type AzureCredentials struct {
	ClientId                   string `json:"clientId"`
	ClientSecret               string `json:"clientSecret"`
	SubscriptionId             string `json:"subscriptionId"`
	TenantId                   string `json:"tenantId"`
	ResourceManagerEndpointUrl string `json:"resourceManagerEndpointUrl"`
}

func (cli *azCli) CreateOrUpdateServicePrincipal(ctx context.Context, subscriptionId string, applicationName string, roleName string) (json.RawMessage, error) {
	// By default the role assignment is tied to the root of the currently active subscription (in the az cli), which may not be the same
	// subscription that the user has requested, so build the scope ourselves.
	scopes := azure.SubscriptionRID(subscriptionId)
	var result ServicePrincipalCredentials

	res, err := cli.runAzCommand(ctx, "ad", "sp", "create-for-rbac", "--scopes", scopes, "--name", applicationName, "--role", roleName, "--output", "json")
	if isNotLoggedInMessage(res.Stderr) {
		return nil, ErrAzCliNotLoggedIn
	} else if err != nil {
		return nil, fmt.Errorf("failed running az ad sp create-for-rbac: %s: %w", res.String(), err)
	}

	if err := json.Unmarshal([]byte(res.Stdout), &result); err != nil {
		return nil, fmt.Errorf("could not unmarshal output %s as a string: %w", res.Stdout, err)
	}

	// --sdk-auth arg was deprecated from the az cli. See: https://docs.microsoft.com/cli/azure/microsoft-graph-migration
	// this argument would ensure that the output from creating a Service Principal could
	// be used as input to log in to azure. See: https://github.com/Azure/login#configure-a-service-principal-with-a-secret
	// Create the credentials expected structure from the json-rawResponse
	credentials := AzureCredentials{
		ClientId:                   result.AppId,
		ClientSecret:               result.Password,
		SubscriptionId:             subscriptionId,
		TenantId:                   result.Tenant,
		ResourceManagerEndpointUrl: "https://management.azure.com/",
	}

	credentialsJson, err := json.Marshal(credentials)
	if err != nil {
		return nil, fmt.Errorf("couldn't build Azure Credential")
	}

	var resultWithAzureCredentialsModel json.RawMessage
	if err := json.Unmarshal(credentialsJson, &resultWithAzureCredentialsModel); err != nil {
		return nil, fmt.Errorf("couldn't build Azure Credential Json")
	}

	return resultWithAzureCredentialsModel, nil
}

func (cli *azCli) GetAccessToken(ctx context.Context) (AzCliAccessToken, error) {
	res, err := cli.runAzCommand(ctx, "account", "get-access-token", "--output", "json")
	if isNotLoggedInMessage(res.Stderr) {
		return AzCliAccessToken{}, ErrAzCliNotLoggedIn
	} else if isRefreshTokenExpiredMessage(res.Stderr) {
		return AzCliAccessToken{}, ErrAzCliRefreshTokenExpired
	} else if err != nil {
		return AzCliAccessToken{}, fmt.Errorf("failed running az account get-access-token: %s: %w", res.String(), err)
	}

	var accessToken AzCliAccessToken
	if err := json.Unmarshal([]byte(res.Stdout), &accessToken); err != nil {
		return AzCliAccessToken{}, fmt.Errorf("could not unmarshal output %s as a AzCliAccessToken: %w", res.Stdout, err)
	}
	return accessToken, nil
}

type GraphQueryRequest struct {
	Subscriptions []string `json:"subscriptions"`
	Query         string   `json:"query"`
}

func (cli *azCli) GraphQuery(ctx context.Context, query string, subscriptions []string) (*AzCliGraphQuery, error) {
	const url = "https://management.azure.com/providers/Microsoft.ResourceGraph/resources?api-version=2021-03-01"

	requestBody := GraphQueryRequest{
		Subscriptions: subscriptions,
		Query:         query,
	}

	requestJson, err := json.Marshal(requestBody)
	if err != nil {
		return nil, fmt.Errorf("marshalling JSON body: %w", err)
	}

	token, err := cli.GetAccessToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("getting access token: %w", err)
	}

	client := http.Client{}
	requestBuffer := bytes.NewBuffer(requestJson)
	request, err := http.NewRequest(http.MethodPost, url, requestBuffer)
	if err != nil {
		return nil, fmt.Errorf("creating http request: %w", err)
	}

	request.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token.AccessToken))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")

	response, err := client.Do(request)
	if err != nil || response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("sending http request: %w", err)
	}

	defer response.Body.Close()
	responseBytes, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, fmt.Errorf("reading http response: %w", err)
	}

	responseText := string(responseBytes)

	var graphQueryResult AzCliGraphQuery
	if err := json.Unmarshal(responseBytes, &graphQueryResult); err != nil {
		return nil, fmt.Errorf("could not unmarshal output '%s' as an AzCliGraphQuery: %w", responseText, err)
	}

	return &graphQueryResult, nil
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

// Azure Active Directory codes can be referenced via https://login.microsoftonline.com/error?code=<ERROR_CODE>,
// where ERROR_CODE is the digits portion of an AAD error code. Example: AADSTS70043 has error code 70043
// Additionally, https://learn.microsoft.com/azure/active-directory/develop/reference-aadsts-error-codes#aadsts-error-codes
// is a helpful resource with a list of error codes and messages.

var isNotLoggedInMessageRegex = regexp.MustCompile(`Please run ('|")az login('|") to (setup account|access your accounts)\.`)

// Regex for the following errors related to refresh tokens:
// - "AADSTS70043: The refresh token has expired or is invalid due to sign-in frequency checks by conditional access.""
// - "AADSTS700082: The refresh token has expired due to inactivity."
var isRefreshTokenExpiredMessageRegex = regexp.MustCompile(`AADSTS(70043|700082)`)

var isResourceSegmentMeNotFoundMessageRegex = regexp.MustCompile(`Resource not found for the segment 'me'.`)

// Regex for "(DeploymentNotFound) Deployment '<name>' could not be found."
var isDeploymentNotFoundMessageRegex = regexp.MustCompile(`\(DeploymentNotFound\)`)

// Regex for "AADSTS700024: Client assertion is not within its valid time range."
var isClientAssertionInvalidMessagedRegex = regexp.MustCompile(`AADSTS700024`)
var isConfigurationIsNotSetMessageRegex = regexp.MustCompile(`Configuration '.*' is not set\.`)
var isDeploymentErrorRegex = regexp.MustCompile(`ERROR: ({.+})`)
var isInnerDeploymentErrorRegex = regexp.MustCompile(`Inner Errors:\s+({.+})`)

func isNotLoggedInMessage(s string) bool {
	return isNotLoggedInMessageRegex.MatchString(s)
}

func isRefreshTokenExpiredMessage(s string) bool {
	return isRefreshTokenExpiredMessageRegex.MatchString(s)
}

func isResourceSegmentMeNotFoundMessage(s string) bool {
	return isResourceSegmentMeNotFoundMessageRegex.MatchString(s)
}

func isDeploymentNotFoundMessage(s string) bool {
	return isDeploymentNotFoundMessageRegex.MatchString(s)
}

func isClientAssertionInvalidMessage(s string) bool {
	return isClientAssertionInvalidMessagedRegex.MatchString(s)
}

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

		commandRunner := exec.GetCommandRunner(ctx)
		args := NewAzCliArgs{
			EnableDebug:     options.EnableDebugLogging,
			EnableTelemetry: options.EnableTelemetry,
			CommandRunner:   commandRunner,
		}
		azCli = NewAzCli(args)
	}

	// Set the user agent if a template has been selected
	template := telemetry.TemplateFromContext(ctx)
	userAgent := azdinternal.MakeUserAgentString(template)
	azCli.SetUserAgent(userAgent)

	return azCli
}
