// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"text/template"

	"azure.ai.projects/internal/azure"
	"azure.ai.projects/internal/exterrors"
	"azure.ai.projects/internal/provisioning"
	"azure.ai.projects/internal/synthesis"

	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	armcognitiveservices "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices/v2"
	armresources "github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/spf13/cobra"
	"go.yaml.in/yaml/v3"
	"google.golang.org/protobuf/types/known/structpb"
)

type projectInitFlags struct {
	projectID       string
	projectEndpoint string
	infra           string
	force           bool
	noPrompt        bool
	requestFile     string
	output          string
}

type resolvedProject struct {
	Mode              projectMode
	ResourceId        string
	SubscriptionId    string
	UserTenantId      string
	ResourceGroupName string
	AccountName       string
	ProjectName       string
	Location          string
	Endpoint          string
	OpenAIEndpoint    string
}

var projectResourceIDPattern = regexp.MustCompile(
	`(?i)^/subscriptions/([^/]+)/resourceGroups/([^/]+)/providers/` +
		`Microsoft\.CognitiveServices/accounts/([^/]+)/projects/([^/]+)$`,
)

const foundryProjectResourceType = "Microsoft.CognitiveServices/accounts/projects"

const (
	foundryTerraformMarker        = ".azd-foundry"
	foundryTerraformMarkerVersion = "terraform-v1\n"
)

// ProjectInitAction implements `azd ai project init`.
type ProjectInitAction struct {
	client *azdext.AzdClient
	flags  *projectInitFlags
	extCtx *azdext.ExtensionContext
}

func newProjectInitCommand(extCtx *azdext.ExtensionContext) *cobra.Command {
	extCtx = ensureExtensionContext(extCtx)
	flags := &projectInitFlags{}
	cmd := &cobra.Command{
		Use:   "init",
		Short: "Initialize or adopt a Microsoft Foundry project.",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			flags.output = extCtx.OutputFormat
			flags.noPrompt = extCtx.NoPrompt
			if flags.requestFile != "" {
				for _, name := range []string{"project-id", "project-endpoint", "infra", "force"} {
					if cmd.Flags().Changed(name) {
						return contractValidationError(
							fmt.Sprintf("--%s cannot be combined with --request-file", name),
						)
					}
				}
			}
			action := &ProjectInitAction{flags: flags, extCtx: extCtx}
			return action.Run(cmd.Context())
		},
	}
	cmd.Flags().StringVar(&flags.projectID, "project-id", "", "Existing Foundry project ARM resource ID")
	cmd.Flags().StringVar(&flags.projectEndpoint, "project-endpoint", "", "Existing Foundry project endpoint")
	cmd.Flags().StringVar(
		&flags.infra, "infra", "", "Eject Bicep or Terraform infrastructure (optional value)",
	)
	_ = cmd.Flags().Lookup("infra").NoOptDefVal
	cmd.Flags().Lookup("infra").NoOptDefVal = provisioning.BicepProviderName
	cmd.Flags().BoolVar(&flags.force, "force", false, "Replace a different configured project")
	registerDelegatedContractFlags(cmd, &flags.requestFile)
	azdext.RegisterFlagOptions(cmd, azdext.FlagOptions{
		Name:          "output",
		AllowedValues: []string{"default", "json", "none"},
		Default:       "default",
		Usage:         "The output format",
	})
	return cmd
}

func (a *ProjectInitAction) Run(ctx context.Context) error {
	if a.flags == nil {
		a.flags = &projectInitFlags{}
	}
	request, err := a.loadRequest()
	if err != nil {
		return err
	}
	if request == nil {
		if a.flags.projectID != "" && a.flags.projectEndpoint != "" {
			return contractValidationError("--project-id and --project-endpoint are mutually exclusive")
		}
		if a.flags.infra != "" {
			if a.flags.infra, err = parseInfraProvider(a.flags.infra); err != nil {
				return err
			}
		}
	}

	client := a.client
	if client == nil {
		client, err = azdext.NewAzdClient()
		if err != nil {
			return exterrors.Dependency(
				exterrors.CodeAzdClientFailed,
				"could not connect to the azd daemon",
				"run this command from an azd extension host",
			)
		}
		defer client.Close()
	}
	a.client = client

	projectRoot := projectRootPath()
	project, _, err := ensureProject(ctx, client, projectRoot)
	if err != nil {
		return err
	}
	projectRoot = project.GetPath()
	if projectRoot == "" {
		projectRoot = projectRootPath()
	}
	envName, err := resolveProjectEnvironmentName(ctx, client, a.environmentName(), projectRoot)
	if err != nil {
		return err
	}
	oldValues, err := currentProjectEnvironment(ctx, client, envName)
	if err != nil {
		return err
	}
	reconciler := &projectServiceReconciler{
		client:            client,
		projectRoot:       projectRoot,
		environmentValues: oldValues,
	}
	service, projectConfig, err := reconciler.discoverProjectService(ctx)
	if err != nil {
		return err
	}

	target, err := resolveProjectTarget(
		ctx, client, projectConfig, service, oldValues, request, a.flags,
	)
	if err != nil {
		return err
	}
	if err := confirmExplicitProjectReplacement(
		ctx, client, target, service, oldValues, request, a.flags,
	); err != nil {
		return err
	}
	if err := resolveAzureContextForInit(
		ctx, client, target, oldValues, allowedLocations(request),
		request != nil && request.ResolveAzureContext,
		a.flags.noPrompt,
	); err != nil {
		return err
	}
	if err := validateAllowedProjectLocation(
		target,
		allowedLocations(request),
		oldValues["AZURE_LOCATION"],
	); err != nil {
		return err
	}
	if target.Mode == projectModeExistingEndpoint {
		if err := validateExistingEndpointMode(
			service,
			target.Endpoint,
			infraFromRequest(request, a.flags),
			projectConfig,
			oldValues,
		); err != nil {
			return err
		}
	} else if err := validateFoundryProvider(projectConfig); err != nil {
		return err
	}
	serviceProjectName := target.ProjectName
	if serviceProjectName == "" {
		serviceProjectName = projectConfig.GetName()
	}
	if err := validateProjectServiceMutation(
		service,
		target.Endpoint,
		infraFromRequest(request, a.flags),
	); err != nil {
		return err
	}
	oldEndpoint := serviceEndpoint(nil)
	if service != nil {
		oldEndpoint = serviceEndpoint(service.Resolved)
	}
	identityChanged := !equalProjectEndpoint(oldEndpoint, target.Endpoint) ||
		!strings.EqualFold(oldValues["AZURE_AI_PROJECT_ID"], target.ResourceId)
	oldProvider, oldPath := projectInfraConfig(projectConfig)
	infraDeclaration, err := readProjectInfraDeclaration(projectConfig)
	if err != nil {
		return err
	}
	providerChanged := target.Mode != projectModeExistingEndpoint &&
		oldProvider == "" && infraDeclaration.layerCount == 0
	if target.Mode != projectModeExistingEndpoint &&
		(projectConfig.GetInfra() == nil || projectConfig.GetInfra().GetProvider() == "") {
		if err := writeFoundryProvider(ctx, client, projectConfig); err != nil {
			return err
		}
	}
	restoreProvider := func() error {
		if !providerChanged {
			return nil
		}
		return restoreProjectInfraConfig(
			ctx, client, oldProvider, oldPath,
		)
	}
	restoreInfra := func() error {
		var restoreErrs []error
		if infra := infraFromRequest(request, a.flags); infra != "" {
			if err := restoreProjectInfraConfig(
				ctx, client, oldProvider, oldPath,
			); err != nil {
				restoreErrs = append(restoreErrs, err)
			}
			if infraDeclaration.layerCount > 0 {
				if err := restoreProjectInfraLayers(
					ctx, client, infraDeclaration.layers,
				); err != nil {
					restoreErrs = append(restoreErrs, err)
				}
			}
		}
		return errors.Join(restoreErrs...)
	}
	serviceName, mutation, restoreService, err := reconciler.reconcileEndpoint(
		ctx, serviceProjectName, target.Endpoint, target.Mode,
	)
	if err != nil {
		return rollbackProjectInit(err, restoreProvider)
	}
	restoreEnvironment, err := reconcileProjectEnvironmentWithRollback(
		ctx, client, envName, target.Mode, target, identityChanged,
	)
	if err != nil {
		return rollbackProjectInit(err, restoreService, restoreProvider)
	}
	if infra := infraFromRequest(request, a.flags); infra != "" {
		if err := ejectProjectInfraWithTarget(
			ctx,
			client,
			projectRoot,
			serviceName,
			infra,
			target.Endpoint,
			target.ResourceId,
			oldValues,
		); err != nil {
			return rollbackProjectInit(
				err, restoreEnvironment, restoreService, restoreInfra,
			)
		}
	}

	result := projectInitOutput{
		SchemaVersion:   delegatedSchemaVersion,
		ProducerVersion: delegatedProducerVersion(),
		ServiceName:     serviceName,
		Mode:            string(target.Mode),
		Mutation:        mutation,
		Endpoint:        target.Endpoint,
		ResourceID:      target.ResourceId,
	}
	if request != nil {
		return nil
	}

	if a.flags.output == "none" {
		return nil
	}
	if a.flags.output == "json" {
		return json.NewEncoder(os.Stdout).Encode(result)
	}
	if mutation == "unchanged" {
		fmt.Printf("Foundry project configuration unchanged (%s).\n", serviceName)
	} else {
		fmt.Printf("Foundry project configuration %s in services.%s.\n", mutation, serviceName)
	}
	return nil
}

func rollbackProjectInit(
	operationErr error,
	rollbacks ...func() error,
) error {
	var rollbackErrs []error
	for _, rollback := range rollbacks {
		if err := rollback(); err != nil {
			rollbackErrs = append(
				rollbackErrs,
				fmt.Errorf("rollback project initialization: %w", err),
			)
		}
	}
	if len(rollbackErrs) == 0 {
		return operationErr
	}
	return errors.Join(append([]error{operationErr}, rollbackErrs...)...)
}

func (a *ProjectInitAction) loadRequest() (*projectInitRequest, error) {
	if a.flags.requestFile == "" {
		return nil, nil
	}
	if err := validateDelegatedFilePath(a.flags.requestFile, "request", true); err != nil {
		return nil, err
	}
	request := &projectInitRequest{}
	if err := decodeDelegatedJSON(a.flags.requestFile, request); err != nil {
		return nil, err
	}
	if err := validateProjectInitRequest(*request); err != nil {
		return nil, err
	}
	if request.Infra.EjectProvider != "" {
		provider, err := parseInfraProvider(request.Infra.EjectProvider)
		if err != nil {
			return nil, err
		}
		request.Infra.EjectProvider = provider
	}
	a.flags.projectID = request.Project.ResourceID
	a.flags.projectEndpoint = request.Project.Endpoint
	a.flags.infra = request.Infra.EjectProvider
	a.flags.force = request.Force
	return request, nil
}

func (a *ProjectInitAction) environmentName() string {
	if a.extCtx != nil {
		return a.extCtx.Environment
	}
	return ""
}

func allowedLocations(request *projectInitRequest) []string {
	if request == nil {
		return nil
	}
	return request.Requirements.AllowedLocations
}

func infraFromRequest(request *projectInitRequest, flags *projectInitFlags) string {
	if request != nil {
		return request.Infra.EjectProvider
	}
	return flags.infra
}

func resolveProjectTarget(
	ctx context.Context,
	client *azdext.AzdClient,
	project *azdext.ProjectConfig,
	service *projectServiceInfo,
	values map[string]string,
	request *projectInitRequest,
	flags *projectInitFlags,
) (*resolvedProject, error) {
	projectID, endpoint := flags.projectID, flags.projectEndpoint
	if request != nil {
		projectID, endpoint = request.Project.ResourceID, request.Project.Endpoint
	}
	if projectID != "" {
		return lookupResolvedProject(ctx, client, projectID)
	}
	if endpoint != "" {
		return resolvedProjectFromEndpoint(endpoint)
	}
	serviceEndpointValue := ""
	if service != nil {
		serviceEndpointValue = serviceEndpoint(service.Resolved)
	}
	envProjectID := values["AZURE_AI_PROJECT_ID"]
	if serviceEndpointValue != "" && envProjectID != "" {
		inferred, err := projectFromResourceID(envProjectID)
		if err != nil {
			return nil, err
		}
		if !equalProjectEndpoint(serviceEndpointValue, inferred.Endpoint) {
			if noPromptForRequest(request, flags) {
				return nil, exterrors.Validation(
					"project_target_mismatch",
					"the configured project endpoint and AZURE_AI_PROJECT_ID identify different projects",
					"rerun with --project-id or --project-endpoint to select the intended project",
				)
			}
			choice, promptErr := client.Prompt().Select(ctx, &azdext.SelectRequest{
				Options: &azdext.SelectOptions{
					Message: "The project service and environment identify different projects. Which should be used?",
					Choices: []*azdext.SelectChoice{
						{Label: "Use the environment project", Value: "environment"},
						{Label: "Keep the configured endpoint", Value: "endpoint"},
					},
				},
			})
			if promptErr != nil {
				return nil, fmt.Errorf("resolve project target mismatch: %w", promptErr)
			}
			if choice.GetValue() == 1 {
				return resolvedProjectFromEndpoint(serviceEndpointValue)
			}
		} else {
			return lookupResolvedProject(ctx, client, envProjectID)
		}
	}
	if envProjectID != "" {
		return lookupResolvedProject(ctx, client, envProjectID)
	}
	if serviceEndpointValue != "" {
		return resolvedProjectFromEndpoint(serviceEndpointValue)
	}
	if noPromptForRequest(request, flags) {
		return &resolvedProject{Mode: projectModeNew}, nil
	}
	return promptProjectTarget(ctx, client, values, allowedLocations(request))
}

func noPromptForRequest(_ *projectInitRequest, flags *projectInitFlags) bool {
	return flags.noPrompt
}

func promptProjectTarget(
	ctx context.Context,
	client *azdext.AzdClient,
	values map[string]string,
	allowed []string,
) (*resolvedProject, error) {
	choices := []*azdext.SelectChoice{
		{Label: "Create a new Foundry project", Value: "new"},
		{Label: "Use an existing Foundry project", Value: "existing"},
	}
	response, err := client.Prompt().Select(ctx, &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message: "Select a Foundry project configuration",
			Choices: choices,
		},
	})
	if err != nil {
		if exterrors.IsCancellation(err) {
			return nil, exterrors.Cancelled("project selection was cancelled")
		}
		return nil, fmt.Errorf("select Foundry project configuration: %w", err)
	}
	index := int(response.GetValue())
	if index < 0 || index >= len(choices) {
		return nil, exterrors.Validation(
			"project_selection_invalid",
			"the project selection response was invalid",
			"retry project initialization",
		)
	}
	if choices[index].GetValue() == "new" {
		return &resolvedProject{Mode: projectModeNew}, nil
	}

	subscriptionID, userTenantID, err := resolveInteractiveSubscription(
		ctx, client, values,
	)
	if err != nil {
		return nil, err
	}
	projects, err := listFoundryProjects(
		ctx, subscriptionID, userTenantID, allowed,
	)
	if err != nil {
		return nil, err
	}
	if len(projects) == 0 {
		return nil, exterrors.Validation(
			"project_not_found",
			"no Foundry projects in the selected subscription satisfy the location restriction",
			"choose a different subscription or create a new project",
		)
	}
	projectChoices := make([]*azdext.SelectChoice, len(projects))
	for i := range projects {
		projectChoices[i] = &azdext.SelectChoice{
			Label: fmt.Sprintf(
				"%s (%s, %s)",
				projects[i].ProjectName,
				projects[i].AccountName,
				projects[i].Location,
			),
			Value: projects[i].ResourceId,
		}
	}
	response, err = client.Prompt().Select(ctx, &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message: "Select an existing Foundry project",
			Choices: projectChoices,
		},
	})
	if err != nil {
		if exterrors.IsCancellation(err) {
			return nil, exterrors.Cancelled("Foundry project selection was cancelled")
		}
		return nil, fmt.Errorf("select existing Foundry project: %w", err)
	}
	index = int(response.GetValue())
	if index < 0 || index >= len(projects) {
		return nil, exterrors.Validation(
			"project_selection_invalid",
			"the Foundry project selection response was invalid",
			"retry project initialization",
		)
	}
	return &projects[index], nil
}

func resolveInteractiveSubscription(
	ctx context.Context,
	client *azdext.AzdClient,
	values map[string]string,
) (string, string, error) {
	subscriptionID := strings.TrimSpace(values["AZURE_SUBSCRIPTION_ID"])
	userTenantID := strings.TrimSpace(values["AZURE_TENANT_ID"])
	if subscriptionID == "" {
		response, err := client.Prompt().PromptSubscription(
			ctx, &azdext.PromptSubscriptionRequest{},
		)
		if err != nil {
			return "", "", fmt.Errorf("select Azure subscription: %w", err)
		}
		if response.GetSubscription() == nil ||
			strings.TrimSpace(response.Subscription.GetId()) == "" {
			return "", "", exterrors.Dependency(
				exterrors.CodeMissingAzureSubscription,
				"no Azure subscription was selected",
				"select an Azure subscription and retry",
			)
		}
		subscriptionID = response.Subscription.GetId()
		userTenantID = response.Subscription.GetUserTenantId()
	} else {
		tenantResponse, err := client.Account().LookupTenant(
			ctx, &azdext.LookupTenantRequest{SubscriptionId: subscriptionID},
		)
		if err != nil {
			return "", "", exterrors.Auth(
				exterrors.CodeTenantLookupFailed,
				fmt.Sprintf(
					"failed to lookup tenant for subscription %s: %s",
					subscriptionID,
					err,
				),
				"verify your Azure login with `azd auth login`",
			)
		}
		if tenantResponse.GetTenantId() != "" {
			userTenantID = tenantResponse.GetTenantId()
		}
	}
	return subscriptionID, userTenantID, nil
}

func listFoundryProjects(
	ctx context.Context,
	subscriptionID, userTenantID string,
	allowed []string,
) ([]resolvedProject, error) {
	credential, err := azidentity.NewAzureDeveloperCLICredential(
		&azidentity.AzureDeveloperCLICredentialOptions{
			TenantID:                   userTenantID,
			AdditionallyAllowedTenants: []string{"*"},
		},
	)
	if err != nil {
		return nil, exterrors.Auth(
			exterrors.CodeCredentialCreationFailed,
			fmt.Sprintf("failed to create Azure credential: %s", err),
			"run `azd auth login` and retry",
		)
	}
	resourcesClient, err := armresources.NewClient(
		subscriptionID, credential, azure.NewArmClientOptions(),
	)
	if err != nil {
		return nil, fmt.Errorf("create Azure resources client: %w", err)
	}
	pager := resourcesClient.NewListPager(&armresources.ClientListOptions{
		Filter: new(fmt.Sprintf("resourceType eq '%s'", foundryProjectResourceType)),
	})
	var projects []resolvedProject
	for pager.More() {
		page, err := pager.NextPage(ctx)
		if err != nil {
			return nil, exterrors.ServiceFromAzure(
				err, exterrors.OpCognitiveAccountList,
			)
		}
		for _, resource := range page.Value {
			if resource == nil || resource.ID == nil {
				continue
			}
			project, err := projectFromResourceID(*resource.ID)
			if err != nil {
				continue
			}
			project.UserTenantId = userTenantID
			if resource.Location != nil {
				project.Location = *resource.Location
			}
			if len(allowed) == 0 ||
				(project.Location != "" && locationAllowed(project.Location, allowed)) {
				projects = append(projects, *project)
			}
		}
	}
	slices.SortFunc(projects, func(left, right resolvedProject) int {
		return strings.Compare(
			strings.ToLower(left.ResourceId),
			strings.ToLower(right.ResourceId),
		)
	})
	return projects, nil
}

func confirmExplicitProjectReplacement(
	ctx context.Context,
	client *azdext.AzdClient,
	target *resolvedProject,
	service *projectServiceInfo,
	values map[string]string,
	request *projectInitRequest,
	flags *projectInitFlags,
) error {
	if target == nil || !explicitProjectTarget(request, flags) || flags.force {
		return nil
	}
	oldEndpoint := serviceEndpoint(nil)
	if service != nil {
		oldEndpoint = serviceEndpoint(service.Resolved)
	}
	oldID := strings.TrimSpace(values["AZURE_AI_PROJECT_ID"])
	if (oldEndpoint == "" && oldID == "") ||
		(oldEndpoint == "" || equalProjectEndpoint(oldEndpoint, target.Endpoint)) &&
			(oldID == "" || strings.EqualFold(oldID, target.ResourceId)) {
		return nil
	}
	if flags.noPrompt {
		return exterrors.Validation(
			"project_replacement_requires_force",
			"the explicit project target differs from the configured project",
			"rerun with --force to replace the configured project in --no-prompt mode",
		)
	}
	choices := []*azdext.SelectChoice{
		{
			Label: "Update the project configuration",
			Value: "update",
		},
		{
			Label: "Cancel",
			Value: "cancel",
		},
	}
	response, err := client.Prompt().Select(ctx, &azdext.SelectRequest{
		Options: &azdext.SelectOptions{
			Message: fmt.Sprintf(
				"Replace the configured project %q with %q?",
				firstNonEmpty(oldEndpoint, oldID),
				firstNonEmpty(target.Endpoint, target.ResourceId),
			),
			Choices: choices,
		},
	})
	if err != nil {
		if exterrors.IsCancellation(err) {
			return exterrors.Cancelled("project replacement was cancelled")
		}
		return fmt.Errorf("confirm project replacement: %w", err)
	}
	if response.GetValue() != 0 {
		return exterrors.Cancelled("project replacement was cancelled")
	}
	return nil
}

func explicitProjectTarget(
	request *projectInitRequest,
	flags *projectInitFlags,
) bool {
	if request != nil {
		return request.Project.ResourceID != "" || request.Project.Endpoint != ""
	}
	return strings.TrimSpace(flags.projectID) != "" ||
		strings.TrimSpace(flags.projectEndpoint) != ""
}

func validateAllowedProjectLocation(
	project *resolvedProject,
	allowed []string,
	fallbackLocation string,
) error {
	if project == nil || len(allowed) == 0 {
		return nil
	}
	location := strings.TrimSpace(project.Location)
	if location == "" {
		location = strings.TrimSpace(fallbackLocation)
	}
	if location == "" {
		return exterrors.Validation(
			"project_location_not_allowed",
			"the project location is unknown and cannot be checked against the allowed locations",
			"provide a project with a known Azure location",
		)
	}
	for _, allowedLocation := range allowed {
		if strings.EqualFold(strings.TrimSpace(allowedLocation), location) {
			return nil
		}
	}
	return exterrors.Validation(
		"project_location_not_allowed",
		fmt.Sprintf("project location %q is outside the allowed locations", location),
		"choose a project in one of the allowed locations",
	)
}

func locationAllowed(location string, allowed []string) bool {
	for _, candidate := range allowed {
		if strings.EqualFold(strings.TrimSpace(candidate), location) {
			return true
		}
	}
	return false
}

func resolveAzureContextForInit(
	ctx context.Context,
	client *azdext.AzdClient,
	target *resolvedProject,
	values map[string]string,
	allowed []string,
	required bool,
	noPrompt bool,
) error {
	if target == nil || target.Mode == projectModeExistingEndpoint {
		return nil
	}
	targetSubscriptionID := strings.TrimSpace(target.SubscriptionId)
	needSubscription := targetSubscriptionID == "" &&
		strings.TrimSpace(values["AZURE_SUBSCRIPTION_ID"]) == ""
	needLocation := strings.TrimSpace(target.Location) == "" &&
		strings.TrimSpace(values["AZURE_LOCATION"]) == ""
	if !required && (noPrompt || (!needSubscription && !needLocation)) {
		return nil
	}
	if noPrompt && (needSubscription || needLocation) {
		missing := make([]string, 0, 2)
		if needSubscription {
			missing = append(missing, "AZURE_SUBSCRIPTION_ID")
		}
		if needLocation {
			missing = append(missing, "AZURE_LOCATION")
		}
		code := exterrors.CodeMissingAzureLocation
		if needSubscription {
			code = exterrors.CodeMissingAzureSubscription
		}
		return exterrors.Dependency(
			code,
			fmt.Sprintf("Azure context is incomplete; missing %s", strings.Join(missing, ", ")),
			"set the missing values in the active azd environment and retry",
		)
	}
	if needSubscription || (needLocation && targetSubscriptionID == "") {
		subscriptionID, userTenantID, err := resolveInteractiveSubscription(
			ctx, client, values,
		)
		if err != nil {
			return err
		}
		target.SubscriptionId = subscriptionID
		target.UserTenantId = userTenantID
	}
	if needLocation {
		azureContext := &azdext.AzureContext{
			Scope: &azdext.AzureScope{
				SubscriptionId: target.SubscriptionId,
				TenantId:       target.UserTenantId,
			},
		}
		response, err := client.Prompt().PromptLocation(ctx, &azdext.PromptLocationRequest{
			AzureContext:     azureContext,
			AllowedLocations: allowed,
		})
		if err != nil {
			return fmt.Errorf("select Azure location: %w", err)
		}
		if response.GetLocation() == nil || response.Location.GetName() == "" {
			return exterrors.Validation(
				"project_location_required",
				"an Azure location is required to create a Foundry project",
				"select an Azure location and retry",
			)
		}
		target.Location = response.Location.GetName()
	}
	return nil
}

func projectFromResourceID(resourceID string) (*resolvedProject, error) {
	resourceID = strings.TrimSpace(resourceID)
	matches := projectResourceIDPattern.FindStringSubmatch(resourceID)
	if len(matches) != 5 {
		return nil, exterrors.Validation(
			"invalid_project_id",
			"the project ID must be a Microsoft.CognitiveServices project resource ID",
			"provide /subscriptions/<id>/resourceGroups/<rg>/providers/"+
				"Microsoft.CognitiveServices/accounts/<account>/projects/<project>",
		)
	}
	canonicalID := fmt.Sprintf(
		"/subscriptions/%s/resourceGroups/%s/providers/Microsoft.CognitiveServices/accounts/%s/projects/%s",
		matches[1], matches[2], matches[3], matches[4],
	)
	return &resolvedProject{
		Mode:              projectModeExistingID,
		ResourceId:        canonicalID,
		SubscriptionId:    matches[1],
		ResourceGroupName: matches[2],
		AccountName:       matches[3],
		ProjectName:       matches[4],
		Endpoint:          fmt.Sprintf("https://%s.services.ai.azure.com/api/projects/%s", matches[3], matches[4]),
		OpenAIEndpoint:    fmt.Sprintf("https://%s.openai.azure.com/", matches[3]),
	}, nil
}

func resolvedProjectFromEndpoint(endpoint string) (*resolvedProject, error) {
	normalized, _, err := validateProjectEndpoint(endpoint)
	if err != nil {
		return nil, err
	}
	parsed := strings.TrimPrefix(normalized, "https://")
	host, path, _ := strings.Cut(parsed, "/")
	account := strings.TrimSuffix(host, ".services.ai.azure.com")
	projectName := ""
	projectPath := "/" + path
	if index := strings.Index(projectPath, projectEndpointPathPrefix); index >= 0 {
		projectName = strings.Trim(
			strings.TrimPrefix(projectPath[index:], projectEndpointPathPrefix),
			"/",
		)
	}
	return &resolvedProject{
		Mode:        projectModeExistingEndpoint,
		AccountName: account,
		ProjectName: projectName,
		Endpoint:    normalized,
	}, nil
}

func lookupResolvedProject(
	ctx context.Context,
	client *azdext.AzdClient,
	resourceID string,
) (*resolvedProject, error) {
	project, err := projectFromResourceID(resourceID)
	if err != nil {
		return nil, err
	}
	tenantResponse, err := client.Account().LookupTenant(ctx,
		&azdext.LookupTenantRequest{SubscriptionId: project.SubscriptionId})
	if err != nil {
		return nil, exterrors.Auth(
			exterrors.CodeTenantLookupFailed,
			fmt.Sprintf("failed to lookup tenant for subscription %s: %s", project.SubscriptionId, err),
			"verify your Azure login with `azd auth login`",
		)
	}
	project.UserTenantId = tenantResponse.GetTenantId()
	credential, err := azidentity.NewAzureDeveloperCLICredential(
		&azidentity.AzureDeveloperCLICredentialOptions{
			TenantID:                   project.UserTenantId,
			AdditionallyAllowedTenants: []string{"*"},
		},
	)
	if err != nil {
		return nil, exterrors.Auth(
			exterrors.CodeCredentialCreationFailed,
			fmt.Sprintf("failed to create Azure credential: %s", err),
			"run `azd auth login` and retry",
		)
	}
	projectsClient, err := armcognitiveservices.NewProjectsClient(
		project.SubscriptionId, credential, azure.NewArmClientOptions(),
	)
	if err != nil {
		return nil, fmt.Errorf("create Foundry projects client: %w", err)
	}
	response, err := projectsClient.Get(ctx,
		project.ResourceGroupName, project.AccountName, project.ProjectName, nil)
	if err != nil {
		return nil, exterrors.ServiceFromAzure(err, exterrors.OpCognitiveAccountList)
	}
	if response.Project.Location != nil {
		project.Location = *response.Project.Location
	}
	return project, nil
}

func projectRootPath() string {
	if root, err := azdext.GetProjectDir(); err == nil && root != "" {
		return root
	}
	if cwd, err := os.Getwd(); err == nil {
		return cwd
	}
	return "."
}

func ensureProject(
	ctx context.Context,
	client *azdext.AzdClient,
	projectRoot string,
) (*azdext.ProjectConfig, bool, error) {
	exists, err := projectFileExists(projectRoot)
	if err != nil {
		return nil, false, err
	}
	if !exists {
		envName := deriveProjectEnvironmentName(projectRoot)
		if err := scaffoldProject(ctx, client, projectRoot, envName); err != nil {
			return nil, false, err
		}
	}

	response, err := client.Project().Get(ctx, &azdext.EmptyRequest{})
	if err != nil {
		return nil, false, fmt.Errorf("load project configuration: %w", err)
	}
	if response.GetProject() != nil {
		if !exists {
			return response.Project, true, nil
		}
		return response.Project, false, nil
	}
	return nil, false, exterrors.Dependency(
		"project_not_found",
		"the azd host returned no project configuration",
		"create an azure.yaml project and retry",
	)
}

func projectFileExists(projectRoot string) (bool, error) {
	for _, name := range []string{"azure.yaml", "azure.yml"} {
		path := filepath.Join(projectRoot, name)
		info, err := os.Stat(path)
		switch {
		case err == nil:
			if !info.IsDir() {
				return true, nil
			}
		case errors.Is(err, fs.ErrNotExist):
			continue
		default:
			return false, fmt.Errorf("check project file %q: %w", path, err)
		}
	}
	return false, nil
}

func scaffoldProject(
	ctx context.Context,
	client *azdext.AzdClient,
	projectRoot string,
	envName string,
) error {
	templateDir, err := os.MkdirTemp(filepath.Dir(projectRoot), ".azd-foundry-template-*")
	if err != nil {
		return fmt.Errorf("create project template directory: %w", err)
	}
	defer os.RemoveAll(templateDir)
	workflow := &azdext.Workflow{
		Name: "init",
		Steps: []*azdext.WorkflowStep{{
			Command: &azdext.WorkflowCommand{Args: []string{
				"init", "-t", templateDir, projectRoot,
				"--environment", envName, "--output=none",
			}},
		}},
	}
	if _, err := client.Workflow().Run(ctx, &azdext.RunWorkflowRequest{Workflow: workflow}); err != nil {
		if errors.Is(err, context.Canceled) {
			return exterrors.Cancelled("project initialization was cancelled")
		}
		return exterrors.Dependency(
			"project_init_failed",
			fmt.Sprintf("failed to initialize project: %s", err),
			"check the project directory is writable and retry",
		)
	}
	return nil
}

func writeFoundryProvider(
	ctx context.Context,
	client *azdext.AzdClient,
	project *azdext.ProjectConfig,
) error {
	if err := validateFoundryProvider(project); err != nil {
		return err
	}
	infraDeclaration, err := readProjectInfraDeclaration(project)
	if err != nil {
		return err
	}
	if project != nil && project.GetInfra() != nil &&
		project.GetInfra().GetProvider() != "" {
		return nil
	}
	if infraDeclaration.layerCount > 0 {
		return nil
	}
	oldProvider, oldPath := projectInfraConfig(project)
	if err := setProjectConfigString(
		ctx,
		client,
		"infra.provider",
		provisioning.FoundryProviderName,
	); err != nil {
		return fmt.Errorf("set Foundry infrastructure provider: %w", err)
	}
	if err := unsetProjectConfigValue(ctx, client, "infra.path"); err != nil {
		operationErr := fmt.Errorf("remove starter infrastructure path: %w", err)
		if restoreErr := restoreProjectInfraConfig(
			ctx,
			client,
			oldProvider,
			oldPath,
		); restoreErr != nil {
			return errors.Join(
				operationErr,
				fmt.Errorf("restore project infrastructure config: %w", restoreErr),
			)
		}
		return operationErr
	}
	return nil
}

func projectInfraConfig(project *azdext.ProjectConfig) (provider, path string) {
	if project == nil || project.GetInfra() == nil {
		return "", ""
	}
	return project.GetInfra().GetProvider(), project.GetInfra().GetPath()
}

func setProjectConfigString(
	ctx context.Context,
	client *azdext.AzdClient,
	path, value string,
) error {
	return setProjectConfigValue(ctx, client, path, value)
}

func setProjectConfigValue(
	ctx context.Context,
	client *azdext.AzdClient,
	path string,
	value any,
) error {
	structValue, err := structpb.NewValue(value)
	if err != nil {
		return err
	}
	_, err = client.Project().SetConfigValue(ctx,
		&azdext.SetProjectConfigValueRequest{
			Path:  path,
			Value: structValue,
		})
	return err
}

func unsetProjectConfigValue(
	ctx context.Context,
	client *azdext.AzdClient,
	path string,
) error {
	_, err := client.Project().UnsetConfig(ctx,
		&azdext.UnsetProjectConfigRequest{Path: path})
	return err
}

func restoreProjectInfraConfig(
	ctx context.Context,
	client *azdext.AzdClient,
	provider, path string,
) error {
	var restoreErrs []error
	if provider == "" {
		if err := unsetProjectConfigValue(ctx, client, "infra.provider"); err != nil {
			restoreErrs = append(restoreErrs, err)
		}
	} else if err := setProjectConfigString(
		ctx,
		client,
		"infra.provider",
		provider,
	); err != nil {
		restoreErrs = append(restoreErrs, err)
	}
	if path == "" {
		if err := unsetProjectConfigValue(ctx, client, "infra.path"); err != nil {
			restoreErrs = append(restoreErrs, err)
		}
	} else if err := setProjectConfigString(
		ctx,
		client,
		"infra.path",
		path,
	); err != nil {
		restoreErrs = append(restoreErrs, err)
	}
	return errors.Join(restoreErrs...)
}

func restoreProjectInfraLayers(
	ctx context.Context,
	client *azdext.AzdClient,
	layers []map[string]any,
) error {
	if layers == nil {
		return unsetProjectConfigValue(ctx, client, "infra.layers")
	}
	values := make([]any, len(layers))
	for i := range layers {
		values[i] = layers[i]
	}
	return setProjectConfigValue(ctx, client, "infra.layers", values)
}

func validateFoundryProvider(project *azdext.ProjectConfig) error {
	infraDeclaration, err := readProjectInfraDeclaration(project)
	if err != nil {
		return err
	}
	if infraDeclaration.layerCount > 0 {
		if infraDeclaration.rootProvider == provisioning.FoundryProviderName {
			return exterrors.Validation(
				"infra_provider_conflict",
				fmt.Sprintf(
					"infra.provider is %q while infra.layers is also declared; the root Foundry provider "+
						"cannot be combined with named layers",
					provisioning.FoundryProviderName,
				),
				"keep one named Foundry layer with provider microsoft.foundry "+
					"instead of setting the root provider",
			)
		}
		if infraDeclaration.foundryLayerCount == 0 {
			if infraDeclaration.ejectedTerraformLayerCount == 1 {
				return nil
			}
			return exterrors.Validation(
				"infra_provider_conflict",
				"azure.yaml declares named infrastructure layers without a Foundry layer",
				"configure exactly one layer with provider microsoft.foundry "+
					"before initializing a Foundry project",
			)
		}
		if infraDeclaration.foundryLayerCount != 1 {
			return exterrors.Validation(
				"infra_provider_conflict",
				"azure.yaml declares more than one microsoft.foundry layer",
				"configure exactly one layer with provider microsoft.foundry "+
					"before initializing a Foundry project",
			)
		}
		return nil
	}
	if project != nil && project.GetInfra() != nil &&
		project.GetInfra().GetProvider() != "" &&
		project.GetInfra().GetProvider() != provisioning.FoundryProviderName {
		return exterrors.Validation(
			"infra_provider_conflict",
			fmt.Sprintf(
				"azure.yaml declares incompatible infrastructure provider %q",
				project.GetInfra().GetProvider(),
			),
			"keep the existing provider or remove it before generating Foundry infrastructure",
		)
	}
	if project != nil && project.GetInfra() != nil &&
		project.GetInfra().GetProvider() != "" {
		return nil
	}

	if project != nil && project.GetInfra() != nil &&
		project.GetInfra().GetPath() != "" {
		infraPath := filepath.Clean(
			filepath.FromSlash(project.GetInfra().GetPath()),
		)
		if infraPath != filepath.Clean(filepath.FromSlash("infra")) {
			return exterrors.Validation(
				"infra_provider_conflict",
				fmt.Sprintf("azure.yaml uses custom infrastructure path %q", project.GetInfra().GetPath()),
				"remove the custom infrastructure path or keep the existing provider",
			)
		}
	}
	if project != nil && project.GetPath() != "" {
		if _, err := os.Stat(filepath.Join(project.GetPath(), "infra")); err == nil {
			return exterrors.Validation(
				"infra_provider_conflict",
				"the project already contains user-owned infra/ files",
				"keep the existing infrastructure provider or remove infra/ explicitly",
			)
		} else if !os.IsNotExist(err) {
			return fmt.Errorf("check project infrastructure: %w", err)
		}
	}
	return nil
}

type projectInfraDeclaration struct {
	rootProvider               string
	rootModule                 string
	layerCount                 int
	foundryLayerCount          int
	ejectedTerraformLayerCount int
	layers                     []map[string]any
	foundryLayer               *projectInfraLayer
}

type projectInfraLayer struct {
	index    int
	name     string
	path     string
	module   string
	provider string
}

func readProjectInfraDeclaration(
	project *azdext.ProjectConfig,
) (projectInfraDeclaration, error) {
	if project == nil || project.GetPath() == "" {
		return projectInfraDeclaration{}, nil
	}
	projectFile, err := projectFilePath(project.GetPath())
	if err != nil {
		return projectInfraDeclaration{}, err
	}
	// #nosec G304
	raw, err := os.ReadFile(projectFile)
	if err != nil {
		return projectInfraDeclaration{}, fmt.Errorf(
			"read %s for infrastructure configuration: %w",
			projectFile,
			err,
		)
	}
	var document struct {
		Infra struct {
			Provider string           `yaml:"provider"`
			Module   string           `yaml:"module"`
			Layers   []map[string]any `yaml:"layers"`
		} `yaml:"infra"`
	}
	if err := yaml.Unmarshal(raw, &document); err != nil {
		return projectInfraDeclaration{}, exterrors.Validation(
			exterrors.CodeInvalidAzureYaml,
			fmt.Sprintf("parse %s infrastructure configuration: %s", projectFile, err),
			"verify azure.yaml is valid YAML",
		)
	}
	declaration := projectInfraDeclaration{
		rootProvider: strings.TrimSpace(document.Infra.Provider),
		rootModule:   strings.TrimSpace(document.Infra.Module),
		layerCount:   len(document.Infra.Layers),
		layers:       document.Infra.Layers,
	}
	for index, layer := range document.Infra.Layers {
		name, _ := layer["name"].(string)
		path, _ := layer["path"].(string)
		module, _ := layer["module"].(string)
		provider, _ := layer["provider"].(string)
		provider = strings.TrimSpace(provider)
		if provider == "" {
			provider = declaration.rootProvider
		}
		if provider == provisioning.FoundryProviderName {
			declaration.foundryLayerCount++
			declaration.foundryLayer = &projectInfraLayer{
				index:    index,
				name:     strings.TrimSpace(name),
				path:     strings.TrimSpace(path),
				module:   strings.TrimSpace(module),
				provider: provider,
			}
		}
		if provider == provisioning.TerraformProviderName &&
			isFoundryTerraformLayer(project.GetPath(), path, module) {
			declaration.ejectedTerraformLayerCount++
		}
	}
	return declaration, nil
}

func isFoundryTerraformLayer(projectRoot, path, module string) bool {
	if projectRoot == "" || path == "" {
		return false
	}
	if module == "" {
		module = "main"
	}
	infraDir := filepath.Join(projectRoot, filepath.FromSlash(path))
	// #nosec G304 -- infraDir is derived from project config.
	marker, err := os.ReadFile(filepath.Join(infraDir, foundryTerraformMarker))
	if err == nil {
		return string(marker) == foundryTerraformMarkerVersion
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return false
	}
	if _, err := os.Stat(filepath.Join(infraDir, module+".tfvars.json")); err != nil {
		return false
	}
	// Support Terraform ejection created before the marker was added.
	// #nosec G304
	main, err := os.ReadFile(filepath.Join(infraDir, "main.tf"))
	if err != nil {
		return false
	}
	source := string(main)
	return strings.Contains(source, `resource "azapi_resource" "foundry_account"`) &&
		strings.Contains(source, `resource "azapi_resource" "project"`)
}

func validateExistingEndpointMode(
	service *projectServiceInfo,
	endpoint string,
	infra string,
	project *azdext.ProjectConfig,
	values map[string]string,
) error {
	if infra != "" {
		return exterrors.Dependency(
			exterrors.CodeInfraEjectRequiresProjectID,
			"infrastructure ejection requires a verified Foundry project resource ID",
			"rerun `azd ai project init --project-id <resource-id> --infra`",
		)
	}
	if hasProjectConnections(project) || hasPendingAcrProvision(values) {
		return exterrors.Dependency(
			"project_reconciliation_requires_project_id",
			"endpoint-only initialization cannot reconcile project connections "+
				"or a pending container registry",
			"rerun `azd ai project init --project-id <resource-id>` "+
				"before retaining project resources",
		)
	}
	if service == nil {
		return nil
	}
	if hasManagedDeployments(service.Resolved) ||
		hasManagedDeployments(service.Raw) {
		return exterrors.Dependency(
			"project_reconciliation_requires_project_id",
			"endpoint-only initialization cannot retain managed model deployments",
			"rerun `azd ai project init --project-id <resource-id>` "+
				"before managing deployments",
		)
	}
	if !equalProjectEndpoint(serviceEndpoint(service.Resolved), endpoint) &&
		hasManagedProjectFields(service.Raw) {
		return exterrors.Dependency(
			"project_reconciliation_requires_project_id",
			"changing the project endpoint would move managed project configuration",
			"rerun `azd ai project init --project-id <resource-id>` before changing project identity",
		)
	}
	return nil
}

func hasProjectConnections(project *azdext.ProjectConfig) bool {
	if project == nil {
		return false
	}
	for _, service := range project.GetServices() {
		if service != nil &&
			strings.EqualFold(
				strings.TrimSpace(service.GetHost()),
				"azure.ai.connection",
			) {
			return true
		}
	}
	return false
}

func hasPendingAcrProvision(values map[string]string) bool {
	if values == nil ||
		strings.TrimSpace(values["AZURE_CONTAINER_REGISTRY_ENDPOINT"]) != "" {
		return false
	}
	for reason := range strings.SplitSeq(values["AI_AGENT_PENDING_PROVISION"], ",") {
		if strings.EqualFold(strings.TrimSpace(reason), "acr") {
			return true
		}
	}
	return false
}

func parseInfraProvider(value string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case provisioning.BicepProviderName:
		return provisioning.BicepProviderName, nil
	case provisioning.TerraformProviderName:
		return provisioning.TerraformProviderName, nil
	default:
		return "", exterrors.Validation(
			exterrors.CodeInvalidParameter,
			fmt.Sprintf("unsupported --infra value %q", value),
			"pass --infra=bicep or --infra=terraform",
		)
	}
}

type projectInfraEjectTarget struct {
	path   string
	module string
	layer  bool
}

func resolveProjectInfraEjectTarget(
	projectRoot string,
	declaration projectInfraDeclaration,
) (projectInfraEjectTarget, error) {
	target := projectInfraEjectTarget{
		path:   "infra",
		module: "main",
	}
	if declaration.layerCount == 0 {
		target.module = normalizeProjectInfraEjectModule(declaration.rootModule)
		if err := validateProjectInfraEjectModule(target.module); err != nil {
			return target, err
		}
		return target, nil
	}
	if declaration.foundryLayerCount != 1 {
		return target, exterrors.Validation(
			"infra_provider_conflict",
			"azure.yaml must declare exactly one microsoft.foundry layer",
			"configure exactly one layer with provider microsoft.foundry "+
				"before ejecting Foundry infrastructure",
		)
	}
	if declaration.foundryLayer == nil {
		return target, exterrors.Validation(
			exterrors.CodeInvalidAzureYaml,
			"azure.yaml does not declare a Foundry infrastructure layer",
			"configure exactly one infra.layers[] entry with "+
				"provider microsoft.foundry",
		)
	}
	target.layer = true
	target.path = declaration.foundryLayer.path
	if target.path == "" {
		return target, exterrors.Validation(
			exterrors.CodeInvalidAzureYaml,
			"the Foundry infrastructure layer must declare a path",
			"set infra.layers[].path to a project-relative directory",
		)
	}
	target.module = normalizeProjectInfraEjectModule(
		declaration.foundryLayer.module,
	)
	if filepath.IsAbs(filepath.FromSlash(target.path)) {
		return target, exterrors.Validation(
			exterrors.CodeInvalidAzureYaml,
			fmt.Sprintf("Foundry infrastructure path %q must be project-relative",
				target.path),
			"set infra.layers[].path to a directory inside the project",
		)
	}
	cleanPath := filepath.Clean(filepath.FromSlash(target.path))
	if cleanPath == "." || cleanPath == ".." ||
		strings.HasPrefix(cleanPath, ".."+string(filepath.Separator)) {
		return target, exterrors.Validation(
			exterrors.CodeInvalidAzureYaml,
			fmt.Sprintf("Foundry infrastructure path %q must be inside the project",
				target.path),
			"set infra.layers[].path to a directory inside the project",
		)
	}
	absRoot, err := filepath.Abs(projectRoot)
	if err != nil {
		return target, fmt.Errorf("resolve project root for infrastructure ejection: %w", err)
	}
	absTarget, err := filepath.Abs(filepath.Join(absRoot, cleanPath))
	if err != nil {
		return target, fmt.Errorf("resolve Foundry infrastructure path: %w", err)
	}
	relative, err := filepath.Rel(absRoot, absTarget)
	if err != nil || relative == "." || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return target, exterrors.Validation(
			exterrors.CodeInvalidAzureYaml,
			fmt.Sprintf("Foundry infrastructure path %q must be inside the project",
				target.path),
			"set infra.layers[].path to a directory inside the project",
		)
	}
	rootReal, err := filepath.EvalSymlinks(absRoot)
	if err != nil {
		return target, fmt.Errorf("resolve project root symlinks: %w", err)
	}
	existingPath := absTarget
	for {
		if _, statErr := os.Lstat(existingPath); statErr == nil {
			break
		} else if !errors.Is(statErr, fs.ErrNotExist) {
			return target, fmt.Errorf(
				"inspect Foundry infrastructure path %q: %w",
				target.path,
				statErr,
			)
		}
		parent := filepath.Dir(existingPath)
		if parent == existingPath {
			break
		}
		existingPath = parent
	}
	existingReal, err := filepath.EvalSymlinks(existingPath)
	if err != nil {
		return target, fmt.Errorf("resolve Foundry infrastructure path: %w", err)
	}
	relative, err = filepath.Rel(rootReal, existingReal)
	if err != nil || relative == ".." ||
		strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return target, exterrors.Validation(
			exterrors.CodeInvalidAzureYaml,
			fmt.Sprintf(
				"Foundry infrastructure path %q escapes the project through a symbolic link",
				target.path,
			),
			"set infra.layers[].path to a directory inside the project",
		)
	}
	if err := validateProjectInfraEjectModule(target.module); err != nil {
		return target, err
	}
	return target, nil
}

func normalizeProjectInfraEjectModule(module string) string {
	if module == "" || filepath.ToSlash(module) == "./main" {
		return "main"
	}
	return module
}

func validateProjectInfraEjectModule(module string) error {
	if module == "" || module == "." || module == ".." ||
		filepath.Base(module) != module ||
		strings.ContainsAny(module, `/\\`) ||
		filepath.Ext(module) != "" {
		return exterrors.Validation(
			exterrors.CodeInvalidAzureYaml,
			fmt.Sprintf("Foundry infrastructure module %q must be a file name "+
				"without an extension", module),
			"set infra.layers[].module to a file name such as main",
		)
	}
	return nil
}

func updateFoundryLayerProvider(
	declaration projectInfraDeclaration,
	provider string,
) ([]map[string]any, error) {
	if declaration.foundryLayer == nil || declaration.layers == nil {
		return nil, fmt.Errorf("Foundry infrastructure layer is not declared")
	}
	updated := make([]map[string]any, len(declaration.layers))
	for index, layer := range declaration.layers {
		cloned, err := cloneMap(layer)
		if err != nil {
			return nil, fmt.Errorf("copy infrastructure layer %d: %w", index, err)
		}
		if index == declaration.foundryLayer.index {
			cloned["provider"] = provider
		}
		updated[index] = cloned
	}
	return updated, nil
}

func ejectProjectInfra(
	ctx context.Context,
	client *azdext.AzdClient,
	projectRoot, serviceName, provider string,
) error {
	projectResponse, projectErr := client.Project().Get(ctx, &azdext.EmptyRequest{})
	if projectErr != nil {
		return fmt.Errorf("read project configuration before infrastructure ejection: %w", projectErr)
	}
	declaration, err := readProjectInfraDeclaration(projectResponse.GetProject())
	if err != nil {
		return err
	}
	if projectResponse.GetProject() != nil &&
		declaration.layerCount == 0 &&
		projectResponse.Project.GetInfra() != nil &&
		projectResponse.Project.Infra.GetProvider() != "" &&
		projectResponse.Project.Infra.GetProvider() != provisioning.FoundryProviderName {
		return exterrors.Validation(
			"infra_provider_conflict",
			fmt.Sprintf(
				"azure.yaml declares incompatible infrastructure provider %q",
				projectResponse.Project.Infra.GetProvider(),
			),
			"remove --infra or change the project to microsoft.foundry explicitly",
		)
	}
	target, err := resolveProjectInfraEjectTarget(projectRoot, declaration)
	if err != nil {
		return err
	}
	oldProvider, oldPath := projectInfraConfig(projectResponse.GetProject())
	projectFile, err := projectFilePath(projectRoot)
	if err != nil {
		return err
	}
	// #nosec G304
	raw, err := os.ReadFile(projectFile)
	if err != nil {
		return fmt.Errorf("read %s for infrastructure ejection: %w", projectFile, err)
	}
	infraDir := filepath.Join(projectRoot, filepath.FromSlash(target.path))
	if _, err := os.Stat(infraDir); err == nil {
		location := "infra/"
		if target.layer {
			location = filepath.ToSlash(target.path)
		}
		return exterrors.Validation(
			"infra_eject_exists",
			fmt.Sprintf(
				"cannot eject Foundry infrastructure because %s already exists",
				location,
			),
			"remove or rename the existing infrastructure directory and retry",
		)
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("check infrastructure directory: %w", err)
	}
	result, err := synthesis.Synthesize(synthesis.Input{
		RawAzureYAML:    raw,
		ServiceName:     serviceName,
		AcceptedHosts:   provisioning.FoundryProvisioningServiceHosts,
		ProjectRoot:     projectRoot,
		PreserveVarRefs: true,
	})
	if err != nil {
		return exterrors.Validation(
			exterrors.CodeInvalidAzureYaml,
			fmt.Sprintf("cannot synthesize Foundry infrastructure: %s", err),
			"fix the project service configuration and retry",
		)
	}
	// #nosec G301
	if err := os.MkdirAll(infraDir, 0755); err != nil {
		return fmt.Errorf("create infra directory: %w", err)
	}
	cleanup := func(operationErr error) error {
		if cleanupErr := os.RemoveAll(infraDir); cleanupErr != nil {
			return errors.Join(
				operationErr,
				fmt.Errorf("remove generated infrastructure: %w", cleanupErr),
			)
		}
		return operationErr
	}
	oldLayers := declaration.layers
	var updatedLayers []map[string]any
	layersChanged := target.layer && provider == provisioning.TerraformProviderName
	if layersChanged {
		updatedLayers, err = updateFoundryLayerProvider(
			declaration,
			provisioning.TerraformProviderName,
		)
		if err != nil {
			return cleanup(err)
		}
	}
	rollback := func(operationErr error) error {
		operationErr = cleanup(operationErr)
		var restoreErr error
		switch {
		case layersChanged:
			restoreErr = restoreProjectInfraLayers(ctx, client, oldLayers)
		case !target.layer:
			restoreErr = restoreProjectInfraConfig(
				ctx,
				client,
				oldProvider,
				oldPath,
			)
		}
		if restoreErr != nil {
			return errors.Join(
				operationErr,
				fmt.Errorf("restore project infrastructure config: %w", restoreErr),
			)
		}
		return operationErr
	}
	if provider == provisioning.TerraformProviderName {
		if result.NetworkMode != synthesis.NetworkModeNone {
			return cleanup(exterrors.Validation(
				"infra_eject_network_unsupported",
				"Terraform ejection does not support the project's network block",
				"eject Bicep instead",
			))
		}
		if err := writeTerraformEjectedInfraAt(
			infraDir,
			result.Parameters,
			target.layer,
			target.module,
		); err != nil {
			return cleanup(err)
		}
		if target.layer {
			if err := setProjectConfigValue(
				ctx,
				client,
				"infra.layers",
				mapsToValues(updatedLayers),
			); err != nil {
				return rollback(fmt.Errorf("stamp Terraform layer provider: %w", err))
			}
		} else {
			if err := setProjectConfigString(
				ctx,
				client,
				"infra.provider",
				provisioning.TerraformProviderName,
			); err != nil {
				return rollback(fmt.Errorf("stamp Terraform provider: %w", err))
			}
			if err := unsetProjectConfigValue(ctx, client, "infra.path"); err != nil {
				return rollback(fmt.Errorf("remove infra.path: %w", err))
			}
		}
	} else {
		if err := copyEmbeddedBicep(infraDir, target.module); err != nil {
			return cleanup(err)
		}
		parameters := map[string]any{"parameters": map[string]any{}}
		for key, value := range result.Parameters {
			parameters["parameters"].(map[string]any)[key] = map[string]any{"value": value}
		}
		if err := writeJSONFile(
			filepath.Join(infraDir, target.module+".parameters.json"),
			parameters,
		); err != nil {
			return cleanup(err)
		}
		if !target.layer {
			if err := unsetProjectConfigValue(ctx, client, "infra.path"); err != nil {
				return rollback(fmt.Errorf("remove infra.path: %w", err))
			}
		}
	}
	return nil
}

func mapsToValues(maps []map[string]any) []any {
	values := make([]any, len(maps))
	for i := range maps {
		values[i] = maps[i]
	}
	return values
}

func projectFilePath(projectRoot string) (string, error) {
	for _, name := range []string{"azure.yaml", "azure.yml"} {
		path := filepath.Join(projectRoot, name)
		info, err := os.Stat(path)
		switch {
		case err == nil && !info.IsDir():
			return path, nil
		case errors.Is(err, fs.ErrNotExist):
			continue
		case err != nil:
			return "", fmt.Errorf("check project file %q: %w", path, err)
		}
	}
	return "", exterrors.Dependency(
		"project_file_not_found",
		"no azure.yaml or azure.yml project file was found",
		"create an azd project before ejecting infrastructure",
	)
}

func copyEmbeddedBicep(destination, module string) error {
	if err := copyEmbeddedTree(
		synthesis.TemplatesFS(),
		"templates",
		destination,
		map[string]struct{}{
			"main.arm.json":       {},
			"brownfield.bicep":    {},
			"brownfield.arm.json": {},
		},
	); err != nil {
		return err
	}
	if module == "" || module == "main" {
		return nil
	}
	return os.Rename(
		filepath.Join(destination, "main.bicep"),
		filepath.Join(destination, module+".bicep"),
	)
}

func writeTerraformEjectedInfra(infraDir string, parameters map[string]any) error {
	return writeTerraformEjectedInfraAt(infraDir, parameters, false, "main")
}

func writeTerraformEjectedInfraAt(
	infraDir string,
	parameters map[string]any,
	layer bool,
	module string,
) error {
	variables, includeAcr, err := terraformEjectionVariables(parameters)
	if err != nil {
		return err
	}
	if err := copyEmbeddedTerraform(infraDir, includeAcr); err != nil {
		return fmt.Errorf("copy Terraform templates: %w", err)
	}
	if err := renderTerraformOutputs(infraDir, includeAcr, layer); err != nil {
		return fmt.Errorf("render Terraform outputs: %w", err)
	}
	if err := writeJSONFile(filepath.Join(infraDir, module+".tfvars.json"), variables); err != nil {
		return fmt.Errorf("write Terraform variables: %w", err)
	}
	if layer {
		// #nosec G306
		if err := os.WriteFile(
			filepath.Join(infraDir, foundryTerraformMarker),
			[]byte(foundryTerraformMarkerVersion),
			0644,
		); err != nil {
			return fmt.Errorf("write Terraform ownership marker: %w", err)
		}
	}
	return nil
}

func terraformEjectionVariables(parameters map[string]any) (map[string]any, bool, error) {
	includeAcr, ok := parameters["includeAcr"].(bool)
	if !ok {
		return nil, false, fmt.Errorf(
			"includeAcr parameter has unexpected type %T",
			parameters["includeAcr"],
		)
	}
	deployments, ok := parameters["deployments"].([]synthesis.Deployment)
	if !ok {
		return nil, false, fmt.Errorf(
			"deployments parameter has unexpected type %T",
			parameters["deployments"],
		)
	}
	connections, ok := parameters["connections"].([]synthesis.Connection)
	if !ok {
		return nil, false, fmt.Errorf(
			"connections parameter has unexpected type %T",
			parameters["connections"],
		)
	}
	credentials, ok := parameters["connectionCredentials"].(map[string]map[string]any)
	if !ok {
		return nil, false, fmt.Errorf(
			"connectionCredentials parameter has unexpected type %T",
			parameters["connectionCredentials"],
		)
	}
	// #nosec G101
	return map[string]any{
		"subscription_id":      "${AZURE_SUBSCRIPTION_ID}",
		"location":             "${AZURE_LOCATION}",
		"resource_group_name":  "${AZURE_RESOURCE_GROUP}",
		"environment_name":     "${AZURE_ENV_NAME}",
		"foundry_project_name": "${AZURE_AI_PROJECT_NAME}",
		"principal_id":         "${AZURE_PRINCIPAL_ID}",
		"resource_token_salt":  "${AZD_RESOURCE_TOKEN_SALT}",
		"deployments":          deployments,
		"connections":          synthesis.JoinConnectionCredentials(connections, credentials),
	}, includeAcr, nil
}

func copyEmbeddedTerraform(destination string, includeAcr bool) error {
	skip := map[string]struct{}{"outputs.tf.tmpl": {}}
	if !includeAcr {
		skip["container-registry.tf"] = struct{}{}
	}
	return copyEmbeddedTree(synthesis.TerraformTemplatesFS(), "templates/terraform", destination,
		skip)
}

func renderTerraformOutputs(destination string, includeAcr, layer bool) error {
	const templatePath = "templates/terraform/outputs.tf.tmpl"
	source, err := fs.ReadFile(synthesis.TerraformTemplatesFS(), templatePath)
	if err != nil {
		return fmt.Errorf("read Terraform outputs template: %w", err)
	}
	tmpl, err := template.New("outputs.tf").Parse(string(source))
	if err != nil {
		return fmt.Errorf("parse Terraform outputs template: %w", err)
	}
	var output bytes.Buffer
	if err := tmpl.Execute(&output, struct {
		IncludeAcr bool
		Layer      bool
	}{IncludeAcr: includeAcr, Layer: layer}); err != nil {
		return fmt.Errorf("render Terraform outputs template: %w", err)
	}
	// #nosec G306
	return os.WriteFile(filepath.Join(destination, "outputs.tf"), output.Bytes(), 0644)
}

func copyEmbeddedTree(files fs.FS, root, destination string, skip map[string]struct{}) error {
	// #nosec G301
	if err := os.MkdirAll(destination, 0755); err != nil {
		return err
	}
	return fs.WalkDir(files, root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		relative, err := filepath.Rel(root, filepath.FromSlash(path))
		if err != nil {
			return err
		}
		target := filepath.Join(destination, relative)
		if entry.IsDir() {
			// #nosec G301
			return os.MkdirAll(target, 0755)
		}
		if _, excluded := skip[filepath.Base(path)]; excluded {
			return nil
		}
		data, err := fs.ReadFile(files, path)
		if err != nil {
			return err
		}
		// #nosec G306
		return os.WriteFile(target, data, 0644)
	})
}

func writeJSONFile(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	// #nosec G306
	return os.WriteFile(path, append(data, '\n'), 0644)
}
