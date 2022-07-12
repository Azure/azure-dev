// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"time"

	azdinternal "github.com/azure/azure-dev/cli/azd/internal"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/executil"
	"github.com/azure/azure-dev/cli/azd/pkg/httpUtil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/internal"
)

var (
	ErrAzCliNotLoggedIn          = errors.New("cli is not logged in")
	ErrCurrentPrincipalIsNotUser = errors.New("current principal is not a user principal")
	ErrClientAssertionExpired    = errors.New("client assertion expired")
	ErrDeploymentNotFound        = errors.New("deployment not found")
	ErrNoConfigurationValue      = errors.New("no value configured")
)

const (
	// CollectTelemetryEnvVarName is the name of the variable that the Azure CLI uses to disable telemetry
	// when you're not using persistent configuration via `az config`
	collectTelemetryEnvVarName = "AZURE_CORE_COLLECT_TELEMETRY"
)

type AzCli interface {
	ExternalTool

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
	ListAccounts(ctx context.Context) ([]AzCliSubscriptionInfo, error)
	ListExtensions(ctx context.Context) ([]AzCliExtensionInfo, error)
	GetCliConfigValue(ctx context.Context, name string) (AzCliConfigValue, error)
	GetSubscriptionTenant(ctx context.Context, subscriptionId string) (string, error)
	GetSubscriptionDeployment(ctx context.Context, subscriptionId string, deploymentName string) (AzCliDeployment, error)
	GetResourceGroupDeployment(ctx context.Context, subscriptionId string, resourceGroupName string, deploymentName string) (AzCliDeployment, error)
	GetKeyVault(ctx context.Context, subscriptionId string, vaultName string) (AzCliKeyVault, error)
	PurgeKeyVault(ctx context.Context, subscriptionId string, vaultName string) error
	DeployAppServiceZip(ctx context.Context, subscriptionId string, resourceGroup string, appName string, deployZipPath string) (string, error)
	DeployFunctionAppUsingZipFile(ctx context.Context, subscriptionID string, resourceGroup string, funcName string, deployZipPath string) (string, error)
	GetFunctionAppProperties(ctx context.Context, subscriptionID string, resourceGroup string, funcName string) (AzCliFunctionAppProperties, error)
	DeployToSubscription(ctx context.Context, subscriptionId string, deploymentName string, templatePath string, parametersPath string, location string) (AzCliDeploymentResult, error)
	DeployToResourceGroup(ctx context.Context, subscriptionId string, resourceGroup string, deploymentName string, templatePath string, parametersPath string) (AzCliDeploymentResult, error)
	DeleteSubscriptionDeployment(ctx context.Context, subscriptionId string, deploymentName string) error
	DeleteResourceGroup(ctx context.Context, subscriptionId string, resourceGroupName string) error
	ListResourceGroupResources(ctx context.Context, subscriptionId string, resourceGroupName string) ([]AzCliResource, error)
	ListSubscriptionDeploymentOperations(ctx context.Context, subscriptionId string, deploymentName string) ([]AzCliResourceOperation, error)
	ListResourceGroupDeploymentOperations(ctx context.Context, subscriptionId string, resourceGroupName string, deploymentName string) ([]AzCliResourceOperation, error)
	// ListAccountLocations lists the physical locations in Azure.
	ListAccountLocations(ctx context.Context) ([]AzCliLocation, error)
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

type AzCliSubscriptionInfo struct {
	Name      string `json:"name"`
	Id        string `json:"id"`
	IsDefault bool   `json:"isDefault"`
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

type AzCliKeyVault struct {
	Id         string `json:"id"`
	Name       string `json:"name"`
	Properties struct {
		EnableSoftDelete      bool `json:"enableSoftDelete"`
		EnablePurgeProtection bool `json:"enablePurgeProtection"`
	} `json:"properties"`
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

type AzCliLocation struct {
	// The human friendly name of the location (e.g. "West US 2")
	DisplayName string `json:"displayName"`
	// The name of the location (e.g. "westus2")
	Name string `json:"name"`
	// The human friendly name of the location, prefixed with a
	// region name (e.g "(US) West US 2")
	RegionalDisplayName string `json:"regionalDisplayName"`
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

func (tok *AzCliAccessToken) UnmarshalJSON(data []byte) error {
	var wire struct {
		AccessToken string `json:"accessToken"`
		ExpiresOn   string `json:"expiresOn"`
	}

	if err := json.Unmarshal(data, &wire); err != nil {
		return fmt.Errorf("unmarshaling json: %w", err)
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
	// RunWithResultFn allows us to stub out the command execution for testing
	RunWithResultFn func(ctx context.Context, args executil.RunArgs) (executil.RunResult, error)
}

func NewAzCli(args NewAzCliArgs) AzCli {
	if args.RunWithResultFn == nil {
		args.RunWithResultFn = executil.RunWithResult
	}

	return &azCli{
		userAgent:       azdinternal.MakeUserAgentString(""),
		enableDebug:     args.EnableDebug,
		enableTelemetry: args.EnableTelemetry,
		runWithResultFn: args.RunWithResultFn,
	}
}

type azCli struct {
	userAgent       string
	enableDebug     bool
	enableTelemetry bool

	// runWithResultFn allows us to stub out the executil.RunWithResult, for testing.
	runWithResultFn func(ctx context.Context, args executil.RunArgs) (executil.RunResult, error)
}

func (cli *azCli) Name() string {
	return "Azure CLI"
}

func (cli *azCli) InstallUrl() string {
	return "https://aka.ms/azure-dev/azure-cli-install"
}

func (cli *azCli) CheckInstalled(_ context.Context) (bool, error) {
	return toolInPath("az")
}

// SetUserAgent sets the user agent that's sent with each call to the Azure
// CLI via the `AZURE_HTTP_USER_AGENT` environment variable.
func (cli *azCli) SetUserAgent(userAgent string) {
	cli.userAgent = userAgent
}

func (cli *azCli) UserAgent() string {
	return cli.userAgent
}

func (cli *azCli) ListAccounts(ctx context.Context) ([]AzCliSubscriptionInfo, error) {
	res, err := cli.runAzCommand(ctx, "account", "list", "--output", "json", "--query", "[].{name:name, id:id, isDefault:isDefault}")

	if isNotLoggedInMessage(res.Stderr) {
		return []AzCliSubscriptionInfo{}, ErrAzCliNotLoggedIn
	} else if err != nil {
		return nil, fmt.Errorf("failed running az account list: %s: %w", res.String(), err)
	}

	var subscriptionInfo []AzCliSubscriptionInfo
	if err := json.Unmarshal([]byte(res.Stdout), &subscriptionInfo); err != nil {
		return nil, fmt.Errorf("could not unmarshal output %s as a []AzCliSubscriptionInfo: %w", res.Stdout, err)
	}
	return subscriptionInfo, nil
}

func (cli *azCli) ListExtensions(ctx context.Context) ([]AzCliExtensionInfo, error) {
	res, err := cli.runAzCommand(ctx, "extension", "list")

	if err != nil {
		return nil, fmt.Errorf("failed running az extension list: %s: %w", res.String(), err)
	}

	var extensionInfo []AzCliExtensionInfo
	if err := json.Unmarshal([]byte(res.Stdout), &extensionInfo); err != nil {
		return nil, fmt.Errorf("could not unmarshal output %s as a []AzCliExtensionInfo: %w", res.Stdout, err)
	}
	return extensionInfo, nil
}

func (cli *azCli) GetSubscriptionTenant(ctx context.Context, subscriptionId string) (string, error) {
	res, err := cli.runAzCommand(ctx, "account", "show", "--subscription", subscriptionId, "--query", "tenantId", "--output", "json")
	if isNotLoggedInMessage(res.Stderr) {
		return "", ErrAzCliNotLoggedIn
	} else if err != nil {
		return "", fmt.Errorf("failed running az account show: %s: %w", res.String(), err)
	}

	var tenantId string
	if err := json.Unmarshal([]byte(res.Stdout), &tenantId); err != nil {
		return "", fmt.Errorf("could not unmarshal output %s as a string: %w", res.Stdout, err)
	}
	return tenantId, nil
}

func (cli *azCli) Login(ctx context.Context, useDeviceCode bool, deviceCodeWriter io.Writer) error {
	args := []string{"login", "--output", "none"}

	var writer io.Writer
	if useDeviceCode {
		writer = deviceCodeWriter
	}

	res, err := cli.runAzCommandWithArgs(ctx, executil.RunArgs{
		Args:   args,
		Stderr: writer,
	})

	if err != nil {
		return fmt.Errorf("failed running az login: %s: %w", res.String(), err)
	}

	return nil
}

func (cli *azCli) LoginAcr(ctx context.Context, subscriptionId string, loginServer string) error {
	res, err := cli.runAzCommand(ctx, "acr", "login", "--subscription", subscriptionId, "--name", loginServer)
	if err != nil {
		return fmt.Errorf("failed registry login for %s: %s: %w", loginServer, res.String(), err)
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
	res, err := cli.runAzCommandWithArgs(context.Background(), executil.RunArgs{
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
	res, err := cli.runAzCommandWithArgs(context.Background(), executil.RunArgs{
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
	res, err := cli.runAzCommandWithArgs(context.Background(), executil.RunArgs{
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
	res, err := cli.runAzCommandWithArgs(context.Background(), executil.RunArgs{
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
	res, err := cli.runAzCommandWithArgs(context.Background(), executil.RunArgs{
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

	return res.Stdout, nil
}

func (cli *azCli) DeployToSubscription(ctx context.Context, subscriptionId string, deploymentName string, templateFile string, parametersFile string, location string) (AzCliDeploymentResult, error) {
	res, err := cli.runAzCommand(ctx, "deployment", "sub", "create", "--subscription", subscriptionId, "--name", deploymentName, "--location", location, "--template-file", templateFile, "--parameters", fmt.Sprintf("@%s", parametersFile), "--output", "json")
	if isNotLoggedInMessage(res.Stderr) {
		return AzCliDeploymentResult{}, ErrAzCliNotLoggedIn
	} else if err != nil {
		if isDeploymentError(res.Stderr) {
			deploymentErrorJson := getDeploymentErrorJson(res.Stderr)
			deploymentError := internal.NewAzureDeploymentError(deploymentErrorJson)
			return AzCliDeploymentResult{}, fmt.Errorf("failed running az deployment sub create: \n%w", deploymentError)
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
		if isDeploymentError(res.Stderr) {
			deploymentErrorJson := getDeploymentErrorJson(res.Stderr)
			deploymentError := internal.NewAzureDeploymentError(deploymentErrorJson)
			return AzCliDeploymentResult{}, fmt.Errorf("failed running az deployment group create: \n%w", deploymentError)
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

func (cli *azCli) DeleteResourceGroup(ctx context.Context, subscriptionId string, resourceGroupName string) error {
	res, err := cli.runAzCommand(ctx, "group", "delete", "--subscription", subscriptionId, "--name", resourceGroupName, "--yes", "--output", "json")
	if isNotLoggedInMessage(res.Stderr) {
		return ErrAzCliNotLoggedIn
	} else if err != nil {
		return fmt.Errorf("failed running az group delete: %s: %w", res.String(), err)
	}

	return nil
}

func (cli *azCli) ListResourceGroupResources(ctx context.Context, subscriptionId string, resourceGroupName string) ([]AzCliResource, error) {
	res, err := cli.runAzCommand(ctx, "resource", "list", "--subscription", subscriptionId, "--resource-group", resourceGroupName, "--output", "json")
	if isNotLoggedInMessage(res.Stderr) {
		return nil, ErrAzCliNotLoggedIn
	} else if err != nil {
		return nil, fmt.Errorf("failed running az resource list: %s: %w", res.String(), err)
	}

	var resources []AzCliResource
	if err := json.Unmarshal([]byte(res.Stdout), &resources); err != nil {
		return nil, fmt.Errorf("could not unmarshal output %s as a []AzCliResource: %w", res.Stdout, err)
	}
	return resources, nil
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

func (cli *azCli) ListAccountLocations(ctx context.Context) ([]AzCliLocation, error) {
	res, err := cli.runAzCommand(ctx, "account", "list-locations", "--query", "[?metadata.regionType == 'Physical']", "--output", "json")
	if isNotLoggedInMessage(res.Stderr) {
		return nil, ErrAzCliNotLoggedIn
	} else if err != nil {
		return nil, fmt.Errorf("failed running az account list-locations: %s: %w", res.String(), err)
	}

	var locations []AzCliLocation
	if err := json.Unmarshal([]byte(res.Stdout), &locations); err != nil {
		return nil, fmt.Errorf("could not unmarshal output %s as a []AzCliLocation: %w", res.Stdout, err)
	}
	return locations, nil
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
	} else if err != nil {
		return AzCliAccessToken{}, fmt.Errorf("failed running az account get-access-token: %s: %w", res.String(), err)
	}

	var accessToken AzCliAccessToken
	if err := json.Unmarshal([]byte(res.Stdout), &accessToken); err != nil {
		return AzCliAccessToken{}, fmt.Errorf("could not unmarshal output %s as a AzCliAccessToken: %w", res.Stdout, err)
	}
	return accessToken, nil
}

func (cli *azCli) GetKeyVault(ctx context.Context, subscriptionId string, vaultName string) (AzCliKeyVault, error) {
	res, err := cli.runAzCommand(ctx, "keyvault", "show", "--subscription", subscriptionId, "--name", vaultName, "--output", "json")
	if isNotLoggedInMessage(res.Stderr) {
		return AzCliKeyVault{}, ErrAzCliNotLoggedIn
	} else if err != nil {
		return AzCliKeyVault{}, fmt.Errorf("failed running az keyvault show: %s: %w", res.String(), err)
	}

	var props AzCliKeyVault
	if err := json.Unmarshal([]byte(res.Stdout), &props); err != nil {
		return AzCliKeyVault{}, fmt.Errorf("could not unmarshal output %s as an AzCliKeyVault: %w", res.Stdout, err)
	}
	return props, nil
}

func (cli *azCli) PurgeKeyVault(ctx context.Context, subscriptionId string, vaultName string) error {
	res, err := cli.runAzCommand(ctx, "keyvault", "purge", "--subscription", subscriptionId, "--name", vaultName, "--output", "json")
	if isNotLoggedInMessage(res.Stderr) {
		return ErrAzCliNotLoggedIn
	} else if err != nil {
		return fmt.Errorf("failed running az keyvault purge: %s: %w", res.String(), err)
	}

	return nil
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

	client := httpUtil.GetHttpUtilFromContext(ctx)
	headers := map[string]string{
		"Authorization": fmt.Sprintf("Bearer %s", token.AccessToken),
	}

	request := &httpUtil.HttpRequestMessage{
		Url:     url,
		Method:  "POST",
		Headers: headers,
		Body:    string(requestJson),
	}

	response, err := client.Send(request)
	if err != nil || response.Status != http.StatusOK {
		return nil, fmt.Errorf("sending http request: %w", err)
	}

	responseText := string(response.Body)

	if isNotLoggedInMessage(responseText) {
		return nil, ErrAzCliNotLoggedIn
	} else if err != nil {
		return nil, fmt.Errorf("failed running az graph query: %s: %w", responseText, err)
	}

	var graphQueryResult AzCliGraphQuery
	if err := json.Unmarshal(response.Body, &graphQueryResult); err != nil {
		return nil, fmt.Errorf("could not unmarshal output %s as an AzCliGraphQuery: %w", responseText, err)
	}

	return &graphQueryResult, nil
}

func (cli *azCli) runAzCommand(ctx context.Context, args ...string) (executil.RunResult, error) {
	return cli.runAzCommandWithArgs(ctx, executil.RunArgs{
		Args: args,
	})
}

// runAzCommandWithArgs will run the 'args', ignoring 'Cmd' in favor of injecting the proper
// 'az' alias.
func (cli *azCli) runAzCommandWithArgs(ctx context.Context, args executil.RunArgs) (executil.RunResult, error) {
	if cli.enableDebug {
		args.Args = append(args.Args, "--debug")
	}

	args.Cmd = "az"
	args.Env = append(args.Env, fmt.Sprintf("AZURE_HTTP_USER_AGENT=%s", cli.userAgent))

	if !cli.enableTelemetry {
		args.Env = append(args.Env, fmt.Sprintf("%s=no", collectTelemetryEnvVarName))
	}

	args.Debug = cli.enableDebug

	return cli.runWithResultFn(ctx, args)
}

var isNotLoggedInMessageRegex = regexp.MustCompile(`Please run ('|")az login('|") to (setup account|access your accounts)\.`)
var isResourceSegmentMeNotFoundMessageRegex = regexp.MustCompile(`Resource not found for the segment 'me'.`)
var isDeploymentNotFoundMessageRegex = regexp.MustCompile(`ERROR: \(DeploymentNotFound\)`)
var isClientAssertionInvalidMessagedRegex = regexp.MustCompile(`ERROR: AADSTS700024: Client assertion is not within its valid time range.`)
var isConfigurationIsNotSetMessageRegex = regexp.MustCompile(`ERROR: Configuration '.*' is not set\.`)
var isDeploymentErrorRegex = regexp.MustCompile(`ERROR: ({.+})`)

func isNotLoggedInMessage(s string) bool {
	return isNotLoggedInMessageRegex.MatchString(s)
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

func isDeploymentError(s string) bool {
	return isDeploymentErrorRegex.MatchString(s)
}

func getDeploymentErrorJson(s string) string {
	matches := isDeploymentErrorRegex.FindStringSubmatch(s)

	if matches == nil {
		return ""
	} else if len(matches) > 1 {
		return matches[1]
	}

	return s
}
