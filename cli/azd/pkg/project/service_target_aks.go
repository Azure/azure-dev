package project

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/azure"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/ext"
	"github.com/azure/azure-dev/cli/azd/pkg/helm"
	"github.com/azure/azure-dev/cli/azd/pkg/input"
	"github.com/azure/azure-dev/cli/azd/pkg/kubelogin"
	"github.com/azure/azure-dev/cli/azd/pkg/kustomize"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/output"
	"github.com/azure/azure-dev/cli/azd/pkg/tools"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/kubectl"
	"github.com/sethvargo/go-retry"
)

const (
	defaultDeploymentPath = "manifests"
)

var (
	featureHelm      alpha.FeatureId = alpha.MustFeatureKey("aks.helm")
	featureKustomize alpha.FeatureId = alpha.MustFeatureKey("aks.kustomize")

	// Finds URLS in the endpoints that contain additional metadata
	// Example: http://10.0.101.18:80 (Service: todo-api, Type: ClusterIP)
	endpointRegex = regexp.MustCompile(`^(.*?)\s*(?:\(.*?\))?$`)
)

// The AKS configuration options
type AksOptions struct {
	// The namespace used for deploying k8s resources. Defaults to the project name
	Namespace string `yaml:"namespace"`
	// The relative folder path from the service that contains the k8s deployment manifests. Defaults to 'manifests'
	DeploymentPath string `yaml:"deploymentPath"`
	// The services ingress configuration options
	Ingress AksIngressOptions `yaml:"ingress"`
	// The services deployment configuration options
	Deployment AksDeploymentOptions `yaml:"deployment"`
	// The services service configuration options
	Service AksServiceOptions `yaml:"service"`
	// The helm configuration options
	Helm *helm.Config `yaml:"helm"`
	// The kustomize configuration options
	Kustomize *kustomize.Config `yaml:"kustomize"`
}

// The AKS ingress options
type AksIngressOptions struct {
	Name         string `yaml:"name"`
	RelativePath string `yaml:"relativePath"`
}

// The AKS deployment options
type AksDeploymentOptions struct {
	Name string `yaml:"name"`
}

// The AKS service configuration options
type AksServiceOptions struct {
	Name string `yaml:"name"`
}

type aksTarget struct {
	env                    *environment.Environment
	envManager             environment.Manager
	console                input.Console
	managedClustersService azapi.ManagedClustersService
	resourceManager        ResourceManager
	kubectl                *kubectl.Cli
	kubeLoginCli           *kubelogin.Cli
	helmCli                *helm.Cli
	kustomizeCli           *kustomize.Cli
	containerHelper        *ContainerHelper
	featureManager         *alpha.FeatureManager
}

// Creates a new instance of the AKS service target
func NewAksTarget(
	env *environment.Environment,
	envManager environment.Manager,
	console input.Console,
	managedClustersService azapi.ManagedClustersService,
	resourceManager ResourceManager,
	kubectlCli *kubectl.Cli,
	kubeLoginCli *kubelogin.Cli,
	helmCli *helm.Cli,
	kustomizeCli *kustomize.Cli,
	containerHelper *ContainerHelper,
	featureManager *alpha.FeatureManager,
) ServiceTarget {
	return &aksTarget{
		env:                    env,
		envManager:             envManager,
		console:                console,
		managedClustersService: managedClustersService,
		resourceManager:        resourceManager,
		kubectl:                kubectlCli,
		kubeLoginCli:           kubeLoginCli,
		helmCli:                helmCli,
		kustomizeCli:           kustomizeCli,
		containerHelper:        containerHelper,
		featureManager:         featureManager,
	}
}

// Gets the required external tools to support the AKS service
func (t *aksTarget) RequiredExternalTools(ctx context.Context, serviceConfig *ServiceConfig) []tools.ExternalTool {
	allTools := []tools.ExternalTool{}
	allTools = append(allTools, t.containerHelper.RequiredExternalTools(ctx, serviceConfig)...)
	allTools = append(allTools, t.kubectl)

	if t.featureManager.IsEnabled(featureHelm) {
		allTools = append(allTools, t.helmCli)
	}

	if t.featureManager.IsEnabled(featureKustomize) {
		allTools = append(allTools, t.kustomizeCli)
	}

	return allTools
}

// Initializes the AKS service target
func (t *aksTarget) Initialize(ctx context.Context, serviceConfig *ServiceConfig) error {
	// Ensure that the k8s context has been configured by the time a deploy operation is performed.
	// We attach to "postprovision" so that any predeploy or postprovision hooks can take advantage of the configuration
	err := serviceConfig.Project.AddHandler(
		"postprovision",
		func(ctx context.Context, args ProjectLifecycleEventArgs) error {
			// Only set the k8s context if we are not in preview mode
			previewMode, has := args.Args["preview"]
			if !has || !previewMode.(bool) {
				return t.setK8sContext(ctx, serviceConfig, "postprovision")
			}

			return nil
		},
	)

	if err != nil {
		return fmt.Errorf("failed adding postprovision handler, %w", err)
	}

	// Ensure that the k8s context has been configured by the time a deploy operation is performed.
	// We attach to "predeploy" so that any predeploy hooks can take advantage of the configuration
	err = serviceConfig.AddHandler("predeploy", func(ctx context.Context, args ServiceLifecycleEventArgs) error {
		return t.setK8sContext(ctx, serviceConfig, "predeploy")
	})

	if err != nil {
		return fmt.Errorf("failed adding predeploy handler, %w", err)
	}

	return nil
}

// Prepares and tags the container image from the build output based on the specified service configuration
func (t *aksTarget) Package(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	packageOutput *ServicePackageResult,
	progress *async.Progress[ServiceProgress],
) (*ServicePackageResult, error) {
	return packageOutput, nil
}

// Deploys service container images to ACR and AKS resources to the AKS cluster
func (t *aksTarget) Deploy(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	packageOutput *ServicePackageResult,
	targetResource *environment.TargetResource,
	progress *async.Progress[ServiceProgress],
) (*ServiceDeployResult, error) {
	if err := t.validateTargetResource(targetResource); err != nil {
		return nil, fmt.Errorf("validating target resource: %w", err)
	}

	if packageOutput == nil {
		return nil, errors.New("missing package output")
	}

	// Only deploy the container image if a package output has been defined
	// Empty package details is a valid scenario for any AKS deployment that does not build any containers
	// Ex) Helm charts, or other manifests that reference external images
	if serviceConfig.Docker.RemoteBuild || packageOutput.Details != nil || packageOutput.PackagePath != "" {
		// Login, tag & push container image to ACR
		_, err := t.containerHelper.Deploy(ctx, serviceConfig, packageOutput, targetResource, true, progress)
		if err != nil {
			return nil, err
		}
	}

	// Sync environment
	t.kubectl.SetEnv(t.env.Dotenv())

	// Deploy k8s resources in the following order:
	// 1. Helm
	// 2. Kustomize
	// 3. Manifests
	//
	// Users may install a helm chart to setup their cluster with custom resource definitions that their
	// custom manifests depend on.
	// Users are more likely to either deploy with kustomize or vanilla manifests but they could do both.

	deployed := false

	// Helm Support
	helmDeployed, err := t.deployHelmCharts(ctx, serviceConfig, progress)
	if err != nil {
		return nil, fmt.Errorf("helm deployment failed: %w", err)
	}

	deployed = deployed || helmDeployed

	// Kustomize Support
	kustomizeDeployed, err := t.deployKustomize(ctx, serviceConfig, progress)
	if err != nil {
		return nil, fmt.Errorf("kustomize deployment failed: %w", err)
	}

	deployed = deployed || kustomizeDeployed

	// Vanilla k8s manifests with minimal templating support
	manifestsDeployed, deployment, err := t.deployManifests(ctx, serviceConfig, progress)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}

	deployed = deployed || manifestsDeployed

	if !deployed {
		return nil, errors.New("no deployment manifests found")
	}

	progress.SetProgress(NewServiceProgress("Fetching endpoints for AKS service"))
	endpoints, err := t.Endpoints(ctx, serviceConfig, targetResource)
	if err != nil {
		return nil, err
	}

	if len(endpoints) > 0 {
		// The AKS endpoints contain some additional identifying information
		// Regex is used to pull the URL ignoring the additional metadata
		// The last endpoint in the array will be the most publicly exposed
		matches := endpointRegex.FindStringSubmatch(endpoints[len(endpoints)-1])
		if len(matches) > 1 {
			t.env.SetServiceProperty(serviceConfig.Name, "ENDPOINT_URL", matches[1])
			if err := t.envManager.Save(ctx, t.env); err != nil {
				return nil, fmt.Errorf("failed updating environment with endpoint url, %w", err)
			}
		}
	}

	return &ServiceDeployResult{
		Package: packageOutput,
		TargetResourceId: azure.KubernetesServiceRID(
			targetResource.SubscriptionId(),
			targetResource.ResourceGroupName(),
			targetResource.ResourceName(),
		),
		Kind:      AksTarget,
		Details:   deployment,
		Endpoints: endpoints,
	}, nil
}

// deployManifests deploys raw or templated yaml manifests to the k8s cluster
func (t *aksTarget) deployManifests(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	task *async.Progress[ServiceProgress],
) (bool, *kubectl.Deployment, error) {
	deploymentPath := serviceConfig.K8s.DeploymentPath
	if deploymentPath == "" {
		deploymentPath = defaultDeploymentPath
	}

	deploymentPath = filepath.Join(serviceConfig.Path(), deploymentPath)

	// Manifests are optional so we will continue if the directory does not exist
	if _, err := os.Stat(deploymentPath); os.IsNotExist(err) {
		return false, nil, err
	}

	task.SetProgress(NewServiceProgress("Applying k8s manifests"))
	err := t.kubectl.Apply(
		ctx,
		deploymentPath,
		nil,
	)
	if err != nil {
		return false, nil, fmt.Errorf("failed applying kube manifests: %w", err)
	}

	deploymentName := serviceConfig.K8s.Deployment.Name
	if deploymentName == "" {
		deploymentName = serviceConfig.Name
	}

	// It is not a requirement for a AZD deploy to contain a deployment object
	// If we don't find any deployment within the namespace we will continue
	task.SetProgress(NewServiceProgress("Verifying deployment"))
	deployment, err := t.waitForDeployment(ctx, deploymentName)
	if err != nil && !errors.Is(err, kubectl.ErrResourceNotFound) {
		// We continue to return a true value here since at this point we have successfully applied the manifests
		// even through the deployment may not have been found
		return true, nil, err
	}

	return true, deployment, nil
}

// deployKustomize deploys kustomize manifests to the k8s cluster
func (t *aksTarget) deployKustomize(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	task *async.Progress[ServiceProgress],
) (bool, error) {
	if serviceConfig.K8s.Kustomize == nil {
		return false, nil
	}

	if !t.featureManager.IsEnabled(featureKustomize) {
		return false, fmt.Errorf(
			"Kustomize support is not enabled. Run '%s' to enable it.",
			alpha.GetEnableCommand(featureKustomize),
		)
	}

	task.SetProgress(NewServiceProgress("Applying k8s manifests with Kustomize"))
	overlayPath, err := serviceConfig.K8s.Kustomize.Directory.Envsubst(t.env.Getenv)
	if err != nil {
		return false, fmt.Errorf("failed to envsubst kustomize directory: %w", err)
	}

	// When deploying with kustomize we need to specify the full path to the kustomize directory.
	// This can either be a base or overlay directory but must contain a kustomization.yaml file
	kustomizeDir := filepath.Join(serviceConfig.Project.Path, serviceConfig.RelativePath, overlayPath)
	if _, err := os.Stat(kustomizeDir); os.IsNotExist(err) {
		return false, fmt.Errorf("kustomize directory '%s' does not exist: %w", kustomizeDir, err)
	}

	// Kustomize does not have a built in way to specify environment variables
	// A common well-known solution is to use the kustomize configMapGenerator within your kustomization.yaml
	// and then generate a .env file that can be used to generate config maps
	// azd can help here to create an .env file from the map specified within azure.yaml kustomize config section
	if len(serviceConfig.K8s.Kustomize.Env) > 0 {
		builder := strings.Builder{}
		for key, exp := range serviceConfig.K8s.Kustomize.Env {
			value, err := exp.Envsubst(t.env.Getenv)
			if err != nil {
				return false, fmt.Errorf("failed to envsubst kustomize env: %w", err)
			}

			builder.WriteString(fmt.Sprintf("%s=%s\n", key, value))
		}

		// We are manually writing the .env file since k8s config maps expect unquoted values
		// The godotenv library will quote values when writing the file without an option to disable
		envFilePath := filepath.Join(kustomizeDir, ".env")
		if err := os.WriteFile(envFilePath, []byte(builder.String()), osutil.PermissionFile); err != nil {
			return false, fmt.Errorf("failed to write kustomize .env: %w", err)
		}

		defer os.Remove(envFilePath)
	}

	// Another common scenario is to use the kustomize edit commands to modify the kustomization.yaml
	// configuration before	applying the manifests.
	// Common scenarios for this would be for modifying the images or namespace used for the deployment
	for _, edit := range serviceConfig.K8s.Kustomize.Edits {
		editArgs, err := edit.Envsubst(t.env.Getenv)
		if err != nil {
			return false, fmt.Errorf("failed to envsubst kustomize edit: %w", err)
		}

		if err := t.kustomizeCli.
			WithCwd(kustomizeDir).
			Edit(ctx, strings.Split(editArgs, " ")...); err != nil {
			return false, err
		}
	}

	// Finally apply manifests with kustomize using the -k flag
	if err := t.kubectl.ApplyWithKustomize(ctx, kustomizeDir, nil); err != nil {
		return false, err
	}

	return true, nil
}

// deployHelmCharts deploys helm charts to the k8s cluster
func (t *aksTarget) deployHelmCharts(
	ctx context.Context, serviceConfig *ServiceConfig,
	task *async.Progress[ServiceProgress],
) (bool, error) {
	if serviceConfig.K8s.Helm == nil {
		return false, nil
	}

	if !t.featureManager.IsEnabled(featureHelm) {
		return false, fmt.Errorf("Helm support is not enabled. Run '%s' to enable it.", alpha.GetEnableCommand(featureHelm))
	}

	for _, repo := range serviceConfig.K8s.Helm.Repositories {
		task.SetProgress(NewServiceProgress(fmt.Sprintf("Configuring helm repo: %s", repo.Name)))
		if err := t.helmCli.AddRepo(ctx, repo); err != nil {
			return false, err
		}

		if err := t.helmCli.UpdateRepo(ctx, repo.Name); err != nil {
			return false, err
		}
	}

	for _, release := range serviceConfig.K8s.Helm.Releases {
		if release.Namespace == "" {
			release.Namespace = t.getK8sNamespace(serviceConfig)
		}

		if err := t.ensureNamespace(ctx, release.Namespace); err != nil {
			return false, err
		}

		task.SetProgress(NewServiceProgress(fmt.Sprintf("Installing helm release: %s", release.Name)))
		if err := t.helmCli.Upgrade(ctx, release); err != nil {
			return false, err
		}

		task.SetProgress(NewServiceProgress(fmt.Sprintf("Checking helm release status: %s", release.Name)))
		err := retry.Do(
			ctx,
			retry.WithMaxDuration(10*time.Minute, retry.NewConstant(5*time.Second)),
			func(ctx context.Context) error {
				status, err := t.helmCli.Status(ctx, release)
				if err != nil {
					return err
				}

				if status.Info.Status != helm.StatusKindDeployed {
					fmt.Printf("Status: %s\n", status.Info.Status)
					return retry.RetryableError(
						fmt.Errorf("helm release '%s' is not ready, %w", release.Name, err),
					)
				}

				return nil
			},
		)

		if err != nil {
			return false, err
		}
	}

	return true, nil
}

// Gets the service endpoints for the AKS service target
func (t *aksTarget) Endpoints(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
) ([]string, error) {
	serviceName := serviceConfig.K8s.Service.Name
	if serviceName == "" {
		serviceName = serviceConfig.Name
	}

	ingressName := serviceConfig.K8s.Service.Name
	if ingressName == "" {
		ingressName = serviceConfig.Name
	}

	// Find endpoints for any matching services
	// These endpoints would typically be internal cluster accessible endpoints
	serviceEndpoints, err := t.getServiceEndpoints(ctx, serviceName)
	if err != nil && !errors.Is(err, kubectl.ErrResourceNotFound) {
		return nil, fmt.Errorf("failed retrieving service endpoints, %w", err)
	}

	// Find endpoints for any matching ingress controllers
	// These endpoints would typically be publicly accessible endpoints
	ingressEndpoints, err := t.getIngressEndpoints(ctx, serviceConfig, ingressName)
	if err != nil && !errors.Is(err, kubectl.ErrResourceNotFound) {
		return nil, fmt.Errorf("failed retrieving ingress endpoints, %w", err)
	}

	endpoints := append(serviceEndpoints, ingressEndpoints...)

	return endpoints, nil
}

func (t *aksTarget) validateTargetResource(
	targetResource *environment.TargetResource,
) error {
	if targetResource.ResourceGroupName() == "" {
		return fmt.Errorf("missing resource group name: %s", targetResource.ResourceGroupName())
	}

	return nil
}

func (t *aksTarget) ensureClusterContext(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
	defaultNamespace string,
) (string, error) {
	kubeConfigPath := t.env.Getenv(kubectl.KubeConfigEnvVarName)
	if kubeConfigPath != "" {
		return kubeConfigPath, nil
	}

	// Login to AKS cluster
	clusterName, err := t.resolveClusterName(serviceConfig, targetResource)
	if err != nil {
		return "", err
	}

	log.Printf("getting AKS credentials for cluster '%s'\n", clusterName)
	clusterCreds, err := t.managedClustersService.GetUserCredentials(
		ctx,
		targetResource.SubscriptionId(),
		targetResource.ResourceGroupName(),
		clusterName,
	)
	if err != nil {
		return "", fmt.Errorf(
			//nolint:lll
			"failed retrieving cluster user credentials. Ensure the current principal has been granted rights to the AKS cluster, %w",
			err,
		)
	}

	if len(clusterCreds.Kubeconfigs) == 0 {
		return "", fmt.Errorf(
			"cluster credentials is empty. Ensure the current principal has been granted rights to the AKS cluster. , %w",
			err,
		)
	}

	// The kubeConfig that we care about will also be at position 0
	// I don't know if there is a valid use case where this credential results would container multiple configs
	kubeConfig, err := kubectl.ParseKubeConfig(ctx, clusterCreds.Kubeconfigs[0].Value)
	if err != nil {
		return "", fmt.Errorf(
			"failed parsing kube config. Ensure your configuration is valid yaml. %w",
			err,
		)
	}

	// Set default namespace for the context
	// This avoids having to specify the namespace for every kubectl command
	kubeConfig.Contexts[0].Context.Namespace = defaultNamespace
	kubeConfigManager, err := kubectl.NewKubeConfigManager(t.kubectl)
	if err != nil {
		return "", err
	}

	// Create or update the kube config/context for the AKS cluster
	kubeConfigPath, err = kubeConfigManager.AddOrUpdateContext(ctx, clusterName, kubeConfig)
	if err != nil {
		return "", fmt.Errorf("failed adding/updating kube context, %w", err)
	}

	// Get the provisioned cluster properties to inspect configuration
	managedCluster, err := t.managedClustersService.Get(
		ctx,
		targetResource.SubscriptionId(),
		targetResource.ResourceGroupName(),
		clusterName,
	)
	if err != nil {
		return "", fmt.Errorf("failed retrieving managed cluster, %w", err)
	}

	azureRbacEnabled := managedCluster.Properties.AADProfile != nil &&
		convert.ToValueWithDefault(managedCluster.Properties.AADProfile.EnableAzureRBAC, false)
	localAccountsDisabled := convert.ToValueWithDefault(managedCluster.Properties.DisableLocalAccounts, false)

	// If we're connecting to a cluster with RBAC enabled and local accounts disabled
	// then we need to convert the kube config to use the exec auth module with azd auth
	if azureRbacEnabled || localAccountsDisabled {
		convertOptions := &kubelogin.ConvertOptions{
			Login:      "azd",
			KubeConfig: kubeConfigPath,
		}

		if err := tools.EnsureInstalled(ctx, t.kubeLoginCli); err != nil {
			return "", err
		}

		if err := t.kubeLoginCli.ConvertKubeConfig(ctx, convertOptions); err != nil {
			return "", err
		}
	}

	// Merge the cluster config/context into the default kube config
	kubeConfigPath, err = kubeConfigManager.MergeConfigs(ctx, "config", clusterName)
	if err != nil {
		return "", err
	}

	// Setup the default kube context to use the AKS cluster context
	if _, err := t.kubectl.ConfigUseContext(ctx, clusterName, nil); err != nil {
		return "", fmt.Errorf(
			"failed setting kube context '%s'. Ensure the specified context exists. %w", clusterName,
			err,
		)
	}

	return kubeConfigPath, nil
}

// Ensures the k8s namespace exists otherwise creates it
func (t *aksTarget) ensureNamespace(ctx context.Context, namespace string) error {
	namespaceResult, err := t.kubectl.CreateNamespace(
		ctx,
		namespace,
		&kubectl.KubeCliFlags{
			DryRun: kubectl.DryRunTypeClient,
			Output: kubectl.OutputTypeYaml,
		},
	)
	if err != nil {
		return fmt.Errorf("failed creating kube namespace: %w", err)
	}

	_, err = t.kubectl.ApplyWithStdIn(ctx, namespaceResult.Stdout, nil)
	if err != nil {
		return fmt.Errorf("failed applying kube namespace: %w", err)
	}

	return nil
}

// Finds a deployment using the specified deploymentNameFilter string
// Waits until the deployment rollout is complete and all replicas are accessible
// Additionally confirms rollout is complete by checking the rollout status
func (t *aksTarget) waitForDeployment(
	ctx context.Context,
	deploymentNameFilter string,
) (*kubectl.Deployment, error) {
	// The deployment can appear like it has succeeded when a previous deployment
	// was already in place.
	deployment, err := kubectl.WaitForResource(
		ctx, t.kubectl, kubectl.ResourceTypeDeployment,
		func(deployment *kubectl.Deployment) bool {
			return strings.Contains(deployment.Metadata.Name, deploymentNameFilter)
		},
		func(deployment *kubectl.Deployment) bool {
			return deployment.Status.AvailableReplicas == deployment.Spec.Replicas
		},
	)

	if err != nil {
		return nil, err
	}

	// Check the rollout status
	// This can be a long operation when the deployment is in a failed state such as an ImagePullBackOff loop
	_, err = t.kubectl.RolloutStatus(ctx, deployment.Metadata.Name, nil)
	if err != nil {
		return nil, err
	}

	return deployment, nil
}

// Finds an ingress using the specified ingressNameFilter string
// Waits until the ingress LoadBalancer has assigned a valid IP address
func (t *aksTarget) waitForIngress(
	ctx context.Context,
	ingressNameFilter string,
) (*kubectl.Ingress, error) {
	return kubectl.WaitForResource(
		ctx, t.kubectl, kubectl.ResourceTypeIngress,
		func(ingress *kubectl.Ingress) bool {
			return strings.Contains(ingress.Metadata.Name, ingressNameFilter)
		},
		func(ingress *kubectl.Ingress) bool {
			for _, config := range ingress.Status.LoadBalancer.Ingress {
				if config.Ip != "" {
					return true
				}
			}

			return false
		},
	)
}

// Finds a service using the specified serviceNameFilter string
// Waits until the service is available
func (t *aksTarget) waitForService(
	ctx context.Context,
	serviceNameFilter string,
) (*kubectl.Service, error) {
	return kubectl.WaitForResource(
		ctx, t.kubectl, kubectl.ResourceTypeService,
		func(service *kubectl.Service) bool {
			return strings.Contains(service.Metadata.Name, serviceNameFilter)
		},
		func(service *kubectl.Service) bool {
			// If the service is not a load balancer it should be immediately available
			if service.Spec.Type != kubectl.ServiceTypeLoadBalancer {
				return true
			}

			// Load balancer can take some time to be provision by AKS
			var ipAddress string
			for _, config := range service.Status.LoadBalancer.Ingress {
				if config.Ip != "" {
					ipAddress = config.Ip
					break
				}
			}

			return ipAddress != ""
		},
	)
}

// Retrieve any service endpoints for the specified serviceNameFilter
// Supports service types for LoadBalancer and ClusterIP
func (t *aksTarget) getServiceEndpoints(
	ctx context.Context,
	serviceNameFilter string,
) ([]string, error) {
	service, err := t.waitForService(ctx, serviceNameFilter)
	if err != nil {
		return nil, err
	}

	var endpoints []string
	if service.Spec.Type == kubectl.ServiceTypeLoadBalancer {
		for _, resource := range service.Status.LoadBalancer.Ingress {
			endpoints = append(
				endpoints,
				fmt.Sprintf("http://%s (Service: %s, Type: LoadBalancer)", resource.Ip, service.Metadata.Name),
			)
		}
	} else if service.Spec.Type == kubectl.ServiceTypeClusterIp {
		for index, ip := range service.Spec.ClusterIps {
			endpoints = append(
				endpoints,
				fmt.Sprintf("http://%s:%d (Service: %s, Type: ClusterIP)",
					ip,
					service.Spec.Ports[index].Port,
					service.Metadata.Name,
				),
			)
		}
	}

	return endpoints, nil
}

// Retrieve any ingress endpoints for the specified serviceNameFilter
// Supports service types for LoadBalancer, supports Hosts and/or IP address
func (t *aksTarget) getIngressEndpoints(
	ctx context.Context,
	serviceConfig *ServiceConfig,
	resourceFilter string,
) ([]string, error) {
	ingress, err := t.waitForIngress(ctx, resourceFilter)
	if err != nil {
		return nil, err
	}

	var endpoints []string
	var protocol string
	if len(ingress.Spec.Tls) == 0 {
		protocol = "http"
	} else {
		protocol = "https"
	}

	for index, resource := range ingress.Status.LoadBalancer.Ingress {
		var baseUrl string
		if ingress.Spec.Rules[index].Host == nil {
			baseUrl = fmt.Sprintf("%s://%s", protocol, resource.Ip)
		} else {
			baseUrl = fmt.Sprintf("%s://%s", protocol, *ingress.Spec.Rules[index].Host)
		}

		endpointUrl, err := url.JoinPath(baseUrl, serviceConfig.K8s.Ingress.RelativePath)
		if err != nil {
			return nil, fmt.Errorf("failed constructing service endpoints, %w", err)
		}

		endpoints = append(endpoints, fmt.Sprintf("%s (Ingress, Type: LoadBalancer)", endpointUrl))
	}

	return endpoints, nil
}

func (t *aksTarget) getK8sNamespace(serviceConfig *ServiceConfig) string {
	namespace := serviceConfig.K8s.Namespace
	if namespace == "" {
		namespace = serviceConfig.Project.Name
	}

	return namespace
}

func (t *aksTarget) setK8sContext(ctx context.Context, serviceConfig *ServiceConfig, eventName ext.Event) error {
	t.kubectl.SetEnv(t.env.Dotenv())
	hasCustomKubeConfig := false

	// If a KUBECONFIG env var is set, use it.
	kubeConfigPath := t.env.Getenv(kubectl.KubeConfigEnvVarName)
	if kubeConfigPath != "" {
		t.kubectl.SetKubeConfig(kubeConfigPath)
		hasCustomKubeConfig = true
	}

	targetResource, err := t.resourceManager.GetTargetResource(ctx, t.env.GetSubscriptionId(), serviceConfig)
	if err != nil {
		return err
	}

	defaultNamespace := t.getK8sNamespace(serviceConfig)
	_, err = t.ensureClusterContext(ctx, serviceConfig, targetResource, defaultNamespace)
	if err != nil {
		return err
	}

	err = t.ensureNamespace(ctx, defaultNamespace)
	if err != nil {
		return err
	}

	// Display message to the user when we detect they are using a non-default KUBECONFIG configuration
	// In standard AZD AKS deployment users should not typically need to set a custom KUBECONFIG
	if hasCustomKubeConfig && eventName == "predeploy" {
		t.console.Message(ctx, output.WithWarningFormat("Using KUBECONFIG @ %s\n", kubeConfigPath))
	}

	return nil
}

// resolveClusterName attempts to resolve the cluster name from the following sources:
// 1. The 'AZD_AKS_CLUSTER' environment variable
// 2. The 'resourceName' property in the azure.yaml (Can use expandable string as well)
// 3. The 'resourceName' property passed the target resource
func (t *aksTarget) resolveClusterName(
	serviceConfig *ServiceConfig,
	targetResource *environment.TargetResource,
) (string, error) {
	// Resolve cluster name
	clusterName, found := t.env.LookupEnv(environment.AksClusterEnvVarName)
	if !found {
		log.Printf("'%s' environment variable not found\n", environment.AksClusterEnvVarName)
	}

	if clusterName == "" {
		yamlClusterName, err := serviceConfig.ResourceName.Envsubst(t.env.Getenv)
		if err != nil {
			log.Println("failed resolving cluster name from `resourceName` in azure.yaml", err)
		}

		clusterName = yamlClusterName
	}

	if clusterName == "" {
		clusterName = targetResource.ResourceName()
	}

	if clusterName == "" {
		return "", fmt.Errorf(
			// nolint:lll
			"could not determine AKS cluster, ensure 'resourceName' is set in your azure.yaml or '%s' environment variable has been set.",
			environment.AksClusterEnvVarName,
		)
	}

	return clusterName, nil
}
