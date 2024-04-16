package devcenter

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/devcentersdk"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/infra/provisioning"
	"go.uber.org/multierr"
	"golang.org/x/exp/slices"
)

// DeploymentFilterPredicate is a predicate function for filtering deployments
type DeploymentFilterPredicate func(d *armresources.DeploymentExtended) bool

// ProjectFilterPredicate is a predicate function for filtering projects
type ProjectFilterPredicate func(p *devcentersdk.Project) bool

// DevCenterFilterPredicate is a predicate function for filtering dev centers
type DevCenterFilterPredicate func(dc *devcentersdk.DevCenter) bool

// EnvironmentDefinitionFilterPredicate is a predicate function for filtering environment definitions
type EnvironmentDefinitionFilterPredicate func(ed *devcentersdk.EnvironmentDefinition) bool

// EnvironmentFilterPredicate is a predicate function for filtering environments
type EnvironmentFilterPredicate func(e *devcentersdk.Environment) bool

type Manager interface {
	// WritableProjects gets a list of ADE projects that a user has write permissions
	WritableProjects(ctx context.Context) ([]*devcentersdk.Project, error)
	// WritableProjectsWithFilter gets a list of ADE projects that a user has write permissions for deployment
	WritableProjectsWithFilter(
		ctx context.Context,
		devCenterFilter DevCenterFilterPredicate,
		projectFilter ProjectFilterPredicate,
	) ([]*devcentersdk.Project, error)
	// Deployment gets the Resource Group scoped deployment for the specified devcenter environment
	Deployment(
		ctx context.Context,
		env *devcentersdk.Environment,
		filter DeploymentFilterPredicate,
	) (infra.Deployment, error)
	// LatestArmDeployment gets the latest ARM deployment for the specified devcenter environment
	LatestArmDeployment(
		ctx context.Context,
		env *devcentersdk.Environment,
		filter DeploymentFilterPredicate,
	) (*armresources.DeploymentExtended, error)
	// Outputs gets the outputs for the specified devcenter environment
	Outputs(
		ctx context.Context,
		env *devcentersdk.Environment,
	) (map[string]provisioning.OutputParameter, error)
}

// Manager provides a common set of methods for interactive with a devcenter and its environments
type manager struct {
	config               *Config
	client               devcentersdk.DevCenterClient
	deploymentsService   azapi.Deployments
	deploymentOperations azapi.DeploymentOperations
	portalUrlBase        string
}

// NewManager creates a new devcenter manager
func NewManager(
	config *Config,
	client devcentersdk.DevCenterClient,
	deploymentsService azapi.Deployments,
	deploymentOperations azapi.DeploymentOperations,
	portalUrlBase string,
) Manager {
	return &manager{
		config:               config,
		client:               client,
		deploymentsService:   deploymentsService,
		deploymentOperations: deploymentOperations,
		portalUrlBase:        string(portalUrlBase),
	}
}

// WritableProjectsWithFilter gets a list of ADE projects that a user has write permissions for deployment
func (m *manager) WritableProjectsWithFilter(
	ctx context.Context,
	devCenterFilter DevCenterFilterPredicate,
	projectFilter ProjectFilterPredicate,
) ([]*devcentersdk.Project, error) {
	writableProjects, err := m.WritableProjects(ctx)
	if err != nil {
		return nil, err
	}

	if devCenterFilter == nil {
		devCenterFilter = func(dc *devcentersdk.DevCenter) bool {
			return true
		}
	}

	if projectFilter == nil {
		projectFilter = func(p *devcentersdk.Project) bool {
			return true
		}
	}

	filteredProjects := []*devcentersdk.Project{}
	for _, project := range writableProjects {
		if devCenterFilter(project.DevCenter) && projectFilter(project) {
			filteredProjects = append(filteredProjects, project)
		}
	}

	return filteredProjects, nil
}

// Gets a list of ADE projects that a user has write permissions
// Write permissions of a project allow the user to create new environment in the project
func (m *manager) WritableProjects(ctx context.Context) ([]*devcentersdk.Project, error) {
	devCenterList, err := m.client.DevCenters().Get(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed getting dev centers: %w", err)
	}

	projectsChan := make(chan *devcentersdk.Project)
	errorsChan := make(chan error)

	// Perform the lookup and checking for projects in parallel to speed up the process
	var wg sync.WaitGroup

	for _, devCenter := range devCenterList.Value {
		wg.Add(1)

		go func(dc *devcentersdk.DevCenter) {
			defer wg.Done()

			projects, err := m.client.
				DevCenterByEndpoint(dc.ServiceUri).
				Projects().
				Get(ctx)

			if err != nil {
				errorsChan <- err
				return
			}

			for _, project := range projects.Value {
				wg.Add(1)

				go func(p *devcentersdk.Project) {
					defer wg.Done()

					hasWriteAccess := m.client.
						DevCenterByEndpoint(p.DevCenter.ServiceUri).
						ProjectByName(p.Name).
						Permissions().
						HasWriteAccess(ctx)

					if hasWriteAccess {
						projectsChan <- p
					}
				}(project)
			}
		}(devCenter)
	}

	go func() {
		wg.Wait()
		close(projectsChan)
		close(errorsChan)
	}()

	var doneGroup sync.WaitGroup
	doneGroup.Add(2)

	var allErrors error
	writeableProjects := []*devcentersdk.Project{}

	go func() {
		defer doneGroup.Done()

		for project := range projectsChan {
			writeableProjects = append(writeableProjects, project)
		}
	}()

	go func() {
		defer doneGroup.Done()

		for err := range errorsChan {
			allErrors = multierr.Append(allErrors, err)
		}
	}()

	// Wait for all the projects and errors to be processed from channels
	doneGroup.Wait()

	if allErrors != nil {
		return nil, allErrors
	}

	return writeableProjects, nil
}

// Deployment gets the Resource Group scoped deployment for the specified devcenter environment
func (m *manager) Deployment(
	ctx context.Context,
	env *devcentersdk.Environment,
	filter DeploymentFilterPredicate,
) (infra.Deployment, error) {
	resourceGroupId, err := devcentersdk.NewResourceGroupId(env.ResourceGroupId)
	if err != nil {
		return nil, fmt.Errorf("failed parsing resource group id: %w", err)
	}

	latestDeployment, err := m.LatestArmDeployment(ctx, env, filter)
	if err != nil {
		return nil, fmt.Errorf("failed getting latest deployment: %w", err)
	}

	return infra.NewResourceGroupDeployment(
		m.deploymentsService,
		m.deploymentOperations,
		resourceGroupId.SubscriptionId,
		resourceGroupId.Name,
		*latestDeployment.Name,
		m.portalUrlBase,
	), nil
}

// LatestArmDeployment gets the latest ARM deployment for the specified devcenter environment
// When a filter is applied the latest deployment that matches the filter will be returned
func (m *manager) LatestArmDeployment(
	ctx context.Context,
	env *devcentersdk.Environment,
	filter DeploymentFilterPredicate,
) (*armresources.DeploymentExtended, error) {
	resourceGroupId, err := devcentersdk.NewResourceGroupId(env.ResourceGroupId)
	if err != nil {
		return nil, fmt.Errorf("failed parsing resource group id: %w", err)
	}

	scope := infra.NewResourceGroupScope(
		m.deploymentsService,
		m.deploymentOperations,
		resourceGroupId.SubscriptionId,
		resourceGroupId.Name,
	)

	deployments, err := scope.ListDeployments(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed listing deployments: %w", err)
	}

	// Sorts the deployments by timestamp in descending order
	slices.SortFunc(deployments, func(x, y *armresources.DeploymentExtended) bool {
		return x.Properties.Timestamp.After(*y.Properties.Timestamp)
	})

	latestDeploymentIndex := slices.IndexFunc(deployments, func(d *armresources.DeploymentExtended) bool {
		tagDevCenterName, devCenterOk := d.Tags[DeploymentTagDevCenterName]
		tagProjectName, projectOk := d.Tags[DeploymentTagDevCenterProject]
		tagEnvTypeName, envTypeOk := d.Tags[DeploymentTagEnvironmentType]
		tagEnvName, envOk := d.Tags[DeploymentTagEnvironmentName]

		// ARM runner deployments contain the deployment tags for the specific environment
		isArmDeployment := devCenterOk && *tagDevCenterName == m.config.Name &&
			projectOk && *tagProjectName == m.config.Project &&
			envTypeOk && *tagEnvTypeName == m.config.EnvironmentType &&
			envOk && *tagEnvName == env.Name

		// Support for untagged Bicep ADE deployments
		// If the deployment is not tagged but starts with the current date and is running
		// this is another indication that this is the latest running Bicep deployment
		isBicepDeployment := !isArmDeployment &&
			strings.HasPrefix(*d.Name, fmt.Sprintf("%s-", time.Now().UTC().Format("2006-01-02")))

		if isArmDeployment || isBicepDeployment {
			if filter == nil {
				return true
			}

			return filter(d)
		}

		return false
	})

	if latestDeploymentIndex == -1 {
		return nil, fmt.Errorf("failed to find latest deployment")
	}

	return deployments[latestDeploymentIndex], nil
}

// Outputs gets the outputs for the latest deployment of the specified environment
// Right now this will retrieve the outputs from the latest azure deployment
// Long term this will call into ADE Outputs API
func (m *manager) Outputs(
	ctx context.Context,
	env *devcentersdk.Environment,
) (map[string]provisioning.OutputParameter, error) {
	resourceGroupId, err := devcentersdk.NewResourceGroupId(env.ResourceGroupId)
	if err != nil {
		return nil, fmt.Errorf("failed parsing resource group id: %w", err)
	}

	latestDeployment, err := m.LatestArmDeployment(ctx, env, func(d *armresources.DeploymentExtended) bool {
		return *d.Properties.ProvisioningState == "Succeeded"
	})
	if err != nil {
		return nil, fmt.Errorf("failed getting latest deployment: %w", err)
	}

	outputs := createOutputParameters(azapi.CreateDeploymentOutput(latestDeployment.Properties.Outputs))

	// Set up AZURE_SUBSCRIPTION_ID and AZURE_RESOURCE_GROUP environment variables
	// These are required for azd deploy to work as expected
	if _, exists := outputs[environment.SubscriptionIdEnvVarName]; !exists {
		outputs[environment.SubscriptionIdEnvVarName] = provisioning.OutputParameter{
			Type:  provisioning.ParameterTypeString,
			Value: resourceGroupId.SubscriptionId,
		}
	}

	if _, exists := outputs[environment.ResourceGroupEnvVarName]; !exists {
		outputs[environment.ResourceGroupEnvVarName] = provisioning.OutputParameter{
			Type:  provisioning.ParameterTypeString,
			Value: resourceGroupId.Name,
		}
	}

	return outputs, nil
}
