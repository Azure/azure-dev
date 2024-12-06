package project

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerregistry/armcontainerregistry"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v2"
	"github.com/azure/azure-dev/cli/azd/pkg/alpha"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/azapi"
	"github.com/azure/azure-dev/cli/azd/pkg/cloud"
	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/containerregistry"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/helm"
	"github.com/azure/azure-dev/cli/azd/pkg/kubelogin"
	"github.com/azure/azure-dev/cli/azd/pkg/kustomize"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/docker"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/dotnet"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/kubectl"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockaccount"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockazsdk"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockenv"
	"github.com/azure/azure-dev/cli/azd/test/ostest"
	"github.com/benbjohnson/clock"
	"github.com/braydonk/yaml"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

func Test_NewAksTarget(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	err := setupMocksForAksTarget(mockContext)
	require.NoError(t, err)

	serviceConfig := createTestServiceConfig("./src/api", AksTarget, ServiceLanguageTypeScript)
	env := createEnv()

	serviceTarget := createAksServiceTarget(mockContext, serviceConfig, env, nil)

	require.NotNil(t, serviceTarget)
	require.NotNil(t, serviceConfig)
}

func Test_Required_Tools(t *testing.T) {
	tempDir := t.TempDir()
	ostest.Chdir(t, tempDir)

	mockContext := mocks.NewMockContext(context.Background())
	err := setupMocksForAksTarget(mockContext)
	require.NoError(t, err)

	serviceConfig := createTestServiceConfig(tempDir, AksTarget, ServiceLanguageTypeScript)
	env := createEnv()

	userConfig := config.NewConfig(nil)
	serviceTarget := createAksServiceTarget(mockContext, serviceConfig, env, userConfig)

	requiredTools := serviceTarget.RequiredExternalTools(*mockContext.Context, serviceConfig)
	require.Len(t, requiredTools, 2)
	require.IsType(t, &docker.Cli{}, requiredTools[0])
	require.IsType(t, &kubectl.Cli{}, requiredTools[1])
}

func Test_Required_Tools_WithAlpha(t *testing.T) {
	tempDir := t.TempDir()
	ostest.Chdir(t, tempDir)

	mockContext := mocks.NewMockContext(context.Background())
	err := setupMocksForAksTarget(mockContext)
	require.NoError(t, err)

	serviceConfig := createTestServiceConfig(tempDir, AksTarget, ServiceLanguageTypeScript)
	env := createEnv()

	userConfig := config.NewConfig(nil)
	_ = userConfig.Set("alpha.aks.helm", "on")
	_ = userConfig.Set("alpha.aks.kustomize", "on")
	serviceTarget := createAksServiceTarget(mockContext, serviceConfig, env, userConfig)

	requiredTools := serviceTarget.RequiredExternalTools(*mockContext.Context, serviceConfig)
	require.Len(t, requiredTools, 4)
	require.IsType(t, &docker.Cli{}, requiredTools[0])
	require.IsType(t, &kubectl.Cli{}, requiredTools[1])
	require.IsType(t, &helm.Cli{}, requiredTools[2])
	require.IsType(t, &kustomize.Cli{}, requiredTools[3])
}

func Test_Package_Deploy_HappyPath(t *testing.T) {
	tempDir := t.TempDir()
	ostest.Chdir(t, tempDir)

	mockContext := mocks.NewMockContext(context.Background())
	err := setupMocksForAksTarget(mockContext)
	require.NoError(t, err)

	serviceConfig := createTestServiceConfig(tempDir, AksTarget, ServiceLanguageTypeScript)
	env := createEnv()

	serviceTarget := createAksServiceTarget(mockContext, serviceConfig, env, nil)
	err = simulateInitliaze(*mockContext.Context, serviceTarget, serviceConfig)
	require.NoError(t, err)

	err = setupK8sManifests(t, serviceConfig)
	require.NoError(t, err)

	packageResult, err := logProgress(t, func(progess *async.Progress[ServiceProgress]) (*ServicePackageResult, error) {
		return serviceTarget.Package(
			*mockContext.Context,
			serviceConfig,
			&ServicePackageResult{
				PackagePath: "test-app/api-test:azd-deploy-0",
				Details: &dockerPackageResult{
					ImageHash:   "IMAGE_HASH",
					TargetImage: "test-app/api-test:azd-deploy-0",
				},
			},
			progess,
		)
	})

	require.NoError(t, err)
	require.NotNil(t, packageResult)
	require.IsType(t, new(dockerPackageResult), packageResult.Details)

	scope := environment.NewTargetResource("SUB_ID", "RG_ID", "", string(azapi.AzureResourceTypeManagedCluster))
	deployResult, err := logProgress(
		t, func(progress *async.Progress[ServiceProgress]) (*ServiceDeployResult, error) {
			return serviceTarget.Deploy(*mockContext.Context, serviceConfig, packageResult, scope, progress)
		},
	)

	require.NoError(t, err)
	require.NotNil(t, deployResult)
	require.Equal(t, AksTarget, deployResult.Kind)
	require.IsType(t, new(kubectl.Deployment), deployResult.Details)
	require.Greater(t, len(deployResult.Endpoints), 0)
	// New env variable is created
	require.Equal(t, "REGISTRY.azurecr.io/test-app/api-test:azd-deploy-0", env.Dotenv()["SERVICE_API_IMAGE_NAME"])
}

func Test_Resolve_Cluster_Name(t *testing.T) {
	tempDir := t.TempDir()
	ostest.Chdir(t, tempDir)

	t.Run("Default env var", func(t *testing.T) {
		tempDir := t.TempDir()
		ostest.Chdir(t, tempDir)

		mockContext := mocks.NewMockContext(context.Background())
		err := setupMocksForAksTarget(mockContext)
		require.NoError(t, err)

		serviceConfig := createTestServiceConfig(tempDir, AksTarget, ServiceLanguageTypeScript)
		env := createEnv()

		serviceTarget := createAksServiceTarget(mockContext, serviceConfig, env, nil)
		err = simulateInitliaze(*mockContext.Context, serviceTarget, serviceConfig)
		require.NoError(t, err)
	})

	t.Run("Simple String", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		err := setupMocksForAksTarget(mockContext)
		require.NoError(t, err)

		serviceConfig := createTestServiceConfig(tempDir, AksTarget, ServiceLanguageTypeScript)
		serviceConfig.ResourceName = osutil.NewExpandableString("AKS_CLUSTER")
		env := createEnv()

		// Remove default AKS cluster name from env file
		env.DotenvDelete(environment.AksClusterEnvVarName)

		serviceTarget := createAksServiceTarget(mockContext, serviceConfig, env, nil)
		err = simulateInitliaze(*mockContext.Context, serviceTarget, serviceConfig)
		require.NoError(t, err)
	})

	t.Run("Expandable String", func(t *testing.T) {
		mockContext := mocks.NewMockContext(context.Background())
		err := setupMocksForAksTarget(mockContext)
		require.NoError(t, err)

		serviceConfig := createTestServiceConfig(tempDir, AksTarget, ServiceLanguageTypeScript)
		serviceConfig.ResourceName = osutil.NewExpandableString("${MY_CUSTOM_ENV_VAR}")
		env := createEnv()
		env.DotenvSet("MY_CUSTOM_ENV_VAR", "AKS_CLUSTER")

		// Remove default AKS cluster name from env file
		env.DotenvDelete(environment.AksClusterEnvVarName)

		serviceTarget := createAksServiceTarget(mockContext, serviceConfig, env, nil)
		err = simulateInitliaze(*mockContext.Context, serviceTarget, serviceConfig)
		require.NoError(t, err)
	})

	t.Run("No Cluster Name", func(t *testing.T) {
		tempDir := t.TempDir()
		ostest.Chdir(t, tempDir)

		mockContext := mocks.NewMockContext(context.Background())
		err := setupMocksForAksTarget(mockContext)
		require.NoError(t, err)

		serviceConfig := createTestServiceConfig(tempDir, AksTarget, ServiceLanguageTypeScript)
		env := createEnv()

		// Simulate AKS cluster name not found in env file
		env.DotenvDelete(environment.AksClusterEnvVarName)

		serviceTarget := createAksServiceTarget(mockContext, serviceConfig, env, nil)
		err = simulateInitliaze(*mockContext.Context, serviceTarget, serviceConfig)
		require.Error(t, err)
		require.ErrorContains(t, err, "could not determine AKS cluster")
	})
}

func Test_Deploy_No_Credentials(t *testing.T) {
	tempDir := t.TempDir()
	ostest.Chdir(t, tempDir)

	mockContext := mocks.NewMockContext(context.Background())
	err := setupMocksForAksTarget(mockContext)
	require.NoError(t, err)

	// Simulate list credentials fail.
	// For more secure clusters getting admin credentials can fail
	err = setupListClusterUserCredentialsMock(mockContext, http.StatusUnauthorized)
	require.NoError(t, err)

	serviceConfig := createTestServiceConfig(tempDir, AksTarget, ServiceLanguageTypeScript)
	env := createEnv()

	serviceTarget := createAksServiceTarget(mockContext, serviceConfig, env, nil)
	err = simulateInitliaze(*mockContext.Context, serviceTarget, serviceConfig)
	require.Error(t, err)
	require.ErrorContains(t, err, "failed retrieving cluster user credentials")
}

func Test_Deploy_Helm(t *testing.T) {
	tempDir := t.TempDir()
	ostest.Chdir(t, tempDir)

	mockContext := mocks.NewMockContext(context.Background())
	err := setupMocksForAksTarget(mockContext)
	require.NoError(t, err)

	mockResults, err := setupMocksForHelm(mockContext)
	require.NoError(t, err)

	serviceConfig := *createTestServiceConfig(tempDir, AksTarget, ServiceLanguageTypeScript)
	serviceConfig.RelativePath = ""
	serviceConfig.K8s.Helm = &helm.Config{
		Repositories: []*helm.Repository{
			{
				Name: "argo",
				Url:  "https://argoproj.github.io/argo-helm",
			},
		},
		Releases: []*helm.Release{
			{
				Name:    "argocd",
				Chart:   "argo/argo-cd",
				Version: "5.51.4",
			},
		},
	}

	env := createEnv()
	userConfig := config.NewConfig(nil)
	_ = userConfig.Set("alpha.aks.helm", "on")

	serviceTarget := createAksServiceTarget(mockContext, &serviceConfig, env, userConfig)
	err = simulateInitliaze(*mockContext.Context, serviceTarget, &serviceConfig)
	require.NoError(t, err)

	packageResult := &ServicePackageResult{
		PackagePath: "",
	}

	scope := environment.NewTargetResource("SUB_ID", "RG_ID", "", string(azapi.AzureResourceTypeManagedCluster))
	deployResult, err := logProgress(
		t, func(progress *async.Progress[ServiceProgress]) (*ServiceDeployResult, error) {
			return serviceTarget.Deploy(*mockContext.Context, &serviceConfig, packageResult, scope, progress)
		},
	)

	require.NoError(t, err)
	require.NotNil(t, deployResult)

	repoAdd, repoAddCalled := mockResults["helm-repo-add"]
	require.True(t, repoAddCalled)
	require.Equal(t, []string{"repo", "add", "argo", "https://argoproj.github.io/argo-helm"}, repoAdd.Args)

	repoUpdate, repoUpdateCalled := mockResults["helm-repo-update"]
	require.True(t, repoUpdateCalled)
	require.Equal(t, []string{"repo", "update", "argo"}, repoUpdate.Args)

	helmUpgrade, helmUpgradeCalled := mockResults["helm-upgrade"]
	require.True(t, helmUpgradeCalled)
	require.Contains(t, strings.Join(helmUpgrade.Args, " "), "upgrade argocd argo/argo-cd")

	helmStatus, helmStatusCalled := mockResults["helm-status"]
	require.True(t, helmStatusCalled)
	require.Contains(t, strings.Join(helmStatus.Args, " "), "status argocd")
}

func Test_Deploy_Kustomize(t *testing.T) {
	tempDir := t.TempDir()
	ostest.Chdir(t, tempDir)

	mockContext := mocks.NewMockContext(context.Background())
	err := setupMocksForAksTarget(mockContext)
	require.NoError(t, err)

	mockResults, err := setupMocksForKustomize(mockContext)
	require.NoError(t, err)

	serviceConfig := *createTestServiceConfig(tempDir, AksTarget, ServiceLanguageTypeScript)
	serviceConfig.RelativePath = ""
	serviceConfig.K8s.Kustomize = &kustomize.Config{
		Directory: osutil.NewExpandableString("./kustomize/overlays/dev"),
		Edits: []osutil.ExpandableString{
			osutil.NewExpandableString("set image todo-api=${SERVICE_API_IMAGE_NAME}"),
		},
	}

	err = os.MkdirAll(filepath.Join(tempDir, "./kustomize/overlays/dev"), osutil.PermissionDirectory)
	require.NoError(t, err)

	env := createEnv()
	env.DotenvSet("SERVICE_API_IMAGE_NAME", "REGISTRY.azurecr.io/test-app/api-test:azd-deploy-0")

	userConfig := config.NewConfig(nil)
	_ = userConfig.Set("alpha.aks.kustomize", "on")

	serviceTarget := createAksServiceTarget(mockContext, &serviceConfig, env, userConfig)
	err = simulateInitliaze(*mockContext.Context, serviceTarget, &serviceConfig)
	require.NoError(t, err)

	packageResult := &ServicePackageResult{
		PackagePath: "",
	}

	scope := environment.NewTargetResource("SUB_ID", "RG_ID", "", string(azapi.AzureResourceTypeManagedCluster))
	deployResult, err := logProgress(
		t, func(progress *async.Progress[ServiceProgress]) (*ServiceDeployResult, error) {
			return serviceTarget.Deploy(*mockContext.Context, &serviceConfig, packageResult, scope, progress)
		},
	)

	require.NoError(t, err)
	require.NotNil(t, deployResult)

	kustomizeEdit, kustomizeEditCalled := mockResults["kustomize-edit"]
	require.True(t, kustomizeEditCalled)
	require.Equal(t, []string{
		"edit",
		"set",
		"image",
		"todo-api=REGISTRY.azurecr.io/test-app/api-test:azd-deploy-0",
	}, kustomizeEdit.Args)

	kubectlApplyKustomize, kubectlApplyKustomizeCalled := mockResults["kubectl-apply-kustomize"]
	require.True(t, kubectlApplyKustomizeCalled)
	require.Equal(t, []string{"apply", "-k", filepath.FromSlash("kustomize/overlays/dev")}, kubectlApplyKustomize.Args)
}

func setupK8sManifests(t *testing.T, serviceConfig *ServiceConfig) error {
	manifestsDir := filepath.Join(serviceConfig.RelativePath, defaultDeploymentPath)
	err := os.MkdirAll(manifestsDir, osutil.PermissionDirectory)
	require.NoError(t, err)

	filenames := []string{"deployment.yaml", "service.yaml", "ingress.yaml"}

	for _, filename := range filenames {
		err = os.WriteFile(filepath.Join(manifestsDir, filename), []byte(""), osutil.PermissionFile)
		require.NoError(t, err)
	}

	return nil
}

func setupMocksForHelm(mockContext *mocks.MockContext) (map[string]exec.RunArgs, error) {
	result := map[string]exec.RunArgs{}

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "helm repo add")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		result["helm-repo-add"] = args
		return exec.NewRunResult(0, "", ""), nil
	})

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "helm repo update")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		result["helm-repo-update"] = args
		return exec.NewRunResult(0, "", ""), nil
	})

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "helm upgrade")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		result["helm-upgrade"] = args
		return exec.NewRunResult(0, "", ""), nil
	})

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "helm status")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		result["helm-status"] = args
		statusResult := `{
			"info": {
				"status": "deployed"
			}
		}`
		return exec.NewRunResult(0, statusResult, ""), nil
	})

	return result, nil
}

func setupMocksForKustomize(mockContext *mocks.MockContext) (map[string]exec.RunArgs, error) {
	result := map[string]exec.RunArgs{}

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "kustomize edit")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		result["kustomize-edit"] = args
		return exec.NewRunResult(0, "", ""), nil
	})

	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "kubectl apply -k")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		result["kubectl-apply-kustomize"] = args
		return exec.NewRunResult(0, "", ""), nil
	})

	return result, nil
}

func setupMocksForAksTarget(mockContext *mocks.MockContext) error {
	err := setupListClusterAdminCredentialsMock(mockContext, http.StatusOK)
	if err != nil {
		return err
	}

	err = setupListClusterUserCredentialsMock(mockContext, http.StatusOK)
	if err != nil {
		return err
	}

	setupGetClusterMock(mockContext, http.StatusOK)
	setupMocksForAcr(mockContext)
	setupMocksForKubectl(mockContext)
	setupMocksForDocker(mockContext)

	return nil
}

func setupGetClusterMock(mockContext *mocks.MockContext, statusCode int) {
	// Get cluster configuration
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && strings.Contains(
			request.URL.Path,
			"Microsoft.ContainerService/managedClusters/AKS_CLUSTER",
		)
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		managedCluster := armcontainerservice.ManagedClustersClientGetResponse{
			ManagedCluster: armcontainerservice.ManagedCluster{
				ID:       to.Ptr("cluster1"),
				Location: to.Ptr("eastus2"),
				Type:     to.Ptr("Microsoft.ContainerService/managedClusters"),
				Properties: &armcontainerservice.ManagedClusterProperties{
					EnableRBAC:           to.Ptr(true),
					DisableLocalAccounts: to.Ptr(false),
				},
			},
		}

		if statusCode == http.StatusOK {
			return mocks.CreateHttpResponseWithBody(request, statusCode, managedCluster)
		} else {
			return mocks.CreateEmptyHttpResponse(request, statusCode)
		}
	})
}

func setupListClusterAdminCredentialsMock(mockContext *mocks.MockContext, statusCode int) error {
	kubeConfig := createTestCluster("cluster1", "user1")
	kubeConfigBytes, err := yaml.Marshal(kubeConfig)
	if err != nil {
		return err
	}

	// Get Admin cluster credentials
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodPost && strings.Contains(request.URL.Path, "listClusterAdminCredential")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		creds := armcontainerservice.CredentialResults{
			Kubeconfigs: []*armcontainerservice.CredentialResult{
				{
					Name:  to.Ptr("context"),
					Value: kubeConfigBytes,
				},
			},
		}

		if statusCode == http.StatusOK {
			return mocks.CreateHttpResponseWithBody(request, statusCode, creds)
		} else {
			return mocks.CreateEmptyHttpResponse(request, statusCode)
		}
	})

	return nil
}

func setupListClusterUserCredentialsMock(mockContext *mocks.MockContext, statusCode int) error {
	kubeConfig := createTestCluster("cluster1", "user1")
	kubeConfigBytes, err := yaml.Marshal(kubeConfig)
	if err != nil {
		return err
	}

	// Get Admin cluster credentials
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodPost && strings.Contains(request.URL.Path, "listClusterUserCredential")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		creds := armcontainerservice.CredentialResults{
			Kubeconfigs: []*armcontainerservice.CredentialResult{
				{
					Name:  to.Ptr("context"),
					Value: kubeConfigBytes,
				},
			},
		}

		if statusCode == http.StatusOK {
			return mocks.CreateHttpResponseWithBody(request, statusCode, creds)
		} else {
			return mocks.CreateEmptyHttpResponse(request, statusCode)
		}
	})

	return nil
}

func setupMocksForAcr(mockContext *mocks.MockContext) {
	mockazsdk.MockContainerRegistryList(mockContext, []*armcontainerregistry.Registry{
		{
			ID: to.Ptr(
				//nolint:lll
				"/subscriptions/SUBSCRIPTION_ID/resourceGroups/RESOURCE_GROUP/providers/Microsoft.ContainerRegistry/registries/REGISTRY",
			),
			Location: to.Ptr("eastus2"),
			Name:     to.Ptr("REGISTRY"),
			Properties: &armcontainerregistry.RegistryProperties{
				LoginServer: to.Ptr("REGISTRY.azurecr.io"),
			},
		},
	})

	mockazsdk.MockContainerRegistryCredentials(mockContext, &armcontainerregistry.RegistryListCredentialsResult{
		Username: to.Ptr("admin"),
		Passwords: []*armcontainerregistry.RegistryPassword{
			{
				Name:  to.Ptr(armcontainerregistry.PasswordName("admin")),
				Value: to.Ptr("password"),
			},
		},
	})

	mockazsdk.MockContainerRegistryTokenExchange(mockContext, "SUBSCRIPTION_ID", "LOGIN_SERVER", "REFRESH_TOKEN")
}

func setupMocksForKubectl(mockContext *mocks.MockContext) {
	// Config view
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "kubectl config view")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "", ""), nil
	})

	// Config use context
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "kubectl config use-context")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "", ""), nil
	})

	// Create Namespace
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "kubectl create namespace")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "", ""), nil
	})

	// Apply With StdIn
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "kubectl apply -f -")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "", ""), nil
	})

	// Apply With File
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "kubectl apply -f")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "", ""), nil
	})

	// Create Secret
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "kubectl create secret generic")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "", ""), nil
	})

	// Get deployments
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "kubectl get deployment")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		deployment := &kubectl.Deployment{
			Resource: kubectl.Resource{
				ApiVersion: "apps/v1",
				Kind:       "Deployment",
				Metadata: kubectl.ResourceMetadata{
					Name:      "api-deployment",
					Namespace: "api-namespace",
				},
			},
			Spec: kubectl.DeploymentSpec{
				Replicas: 2,
			},
			Status: kubectl.DeploymentStatus{
				AvailableReplicas: 2,
				ReadyReplicas:     2,
				Replicas:          2,
				UpdatedReplicas:   2,
			},
		}
		deploymentList := createK8sResourceList(deployment)
		jsonBytes, _ := json.Marshal(deploymentList)

		return exec.NewRunResult(0, string(jsonBytes), ""), nil
	})

	// Rollout status
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "kubectl rollout status")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "", ""), nil
	})

	// Get services
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "kubectl get svc")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		service := &kubectl.Service{
			Resource: kubectl.Resource{
				ApiVersion: "v1",
				Kind:       "Service",
				Metadata: kubectl.ResourceMetadata{
					Name:      "api-service",
					Namespace: "api-namespace",
				},
			},
			Spec: kubectl.ServiceSpec{
				Type: kubectl.ServiceTypeClusterIp,
				ClusterIps: []string{
					"10.10.10.10",
				},
				Ports: []kubectl.Port{
					{
						Port:       80,
						TargetPort: 3000,
						Protocol:   "http",
					},
				},
			},
		}
		serviceList := createK8sResourceList(service)
		jsonBytes, _ := json.Marshal(serviceList)

		return exec.NewRunResult(0, string(jsonBytes), ""), nil
	})

	// Get Ingress
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "kubectl get ing")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		ingress := &kubectl.Ingress{
			Resource: kubectl.Resource{
				ApiVersion: "networking.k8s.io/v1",
				Kind:       "Ingress",
				Metadata: kubectl.ResourceMetadata{
					Name:      "api-ingress",
					Namespace: "api-namespace",
				},
			},
			Spec: kubectl.IngressSpec{
				IngressClassName: "webapprouting.kubernetes.azure.com",
				Rules: []kubectl.IngressRule{
					{
						Http: kubectl.IngressRuleHttp{
							Paths: []kubectl.IngressPath{
								{
									Path:     "/",
									PathType: "Prefix",
								},
							},
						},
					},
				},
			},
			Status: kubectl.IngressStatus{
				LoadBalancer: kubectl.LoadBalancer{
					Ingress: []kubectl.LoadBalancerIngress{
						{
							Ip: "1.1.1.1",
						},
					},
				},
			},
		}
		ingressList := createK8sResourceList(ingress)
		jsonBytes, _ := json.Marshal(ingressList)

		return exec.NewRunResult(0, string(jsonBytes), ""), nil
	})
}

func setupMocksForDocker(mockContext *mocks.MockContext) {
	// Docker login
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "docker login")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "", ""), nil
	})

	// Docker Pull
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "docker pull")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "", ""), nil
	})

	// Docker Tag
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "docker tag")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "", ""), nil
	})

	// Push Container Image
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "docker push")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "", ""), nil
	})
}

func createK8sResourceList[T any](resource T) *kubectl.List[T] {
	return &kubectl.List[T]{
		Resource: kubectl.Resource{
			ApiVersion: "list",
			Kind:       "List",
			Metadata: kubectl.ResourceMetadata{
				Name:      "list",
				Namespace: "namespace",
			},
		},
		Items: []T{
			resource,
		},
	}
}

func createEnv() *environment.Environment {
	return environment.NewWithValues("test", map[string]string{
		environment.TenantIdEnvVarName:                  "TENANT_ID",
		environment.SubscriptionIdEnvVarName:            "SUBSCRIPTION_ID",
		environment.LocationEnvVarName:                  "LOCATION",
		environment.ResourceGroupEnvVarName:             "RESOURCE_GROUP",
		environment.AksClusterEnvVarName:                "AKS_CLUSTER",
		environment.ContainerRegistryEndpointEnvVarName: "REGISTRY.azurecr.io",
	})
}

func createAksServiceTarget(
	mockContext *mocks.MockContext,
	serviceConfig *ServiceConfig,
	env *environment.Environment,
	userConfig config.Config,
) ServiceTarget {
	kubeCtl := kubectl.NewCli(mockContext.CommandRunner)
	helmCli := helm.NewCli(mockContext.CommandRunner)
	kustomizeCli := kustomize.NewCli(mockContext.CommandRunner)
	dockerCli := docker.NewCli(mockContext.CommandRunner)
	dotnetCli := dotnet.NewCli(mockContext.CommandRunner)
	kubeLoginCli := kubelogin.NewCli(mockContext.CommandRunner)
	credentialProvider := mockaccount.SubscriptionCredentialProviderFunc(
		func(_ context.Context, _ string) (azcore.TokenCredential, error) {
			return mockContext.Credentials, nil
		})

	envManager := &mockenv.MockEnvManager{}
	envManager.On("Save", *mockContext.Context, env).Return(nil)

	resourceManager := &MockResourceManager{}
	targetResource := environment.NewTargetResource(
		"SUBSCRIPTION_ID",
		"RESOURCE_GROUP",
		"",
		string(azapi.AzureResourceTypeManagedCluster),
	)
	resourceManager.
		On("GetTargetResource", *mockContext.Context, "SUBSCRIPTION_ID", serviceConfig).
		Return(targetResource, nil)

	managedClustersService := azcli.NewManagedClustersService(credentialProvider, mockContext.ArmClientOptions)
	containerRegistryService := azcli.NewContainerRegistryService(
		credentialProvider,
		dockerCli,
		mockContext.ArmClientOptions,
		mockContext.CoreClientOptions,
	)
	remoteBuildManager := containerregistry.NewRemoteBuildManager(
		credentialProvider,
		mockContext.ArmClientOptions,
	)
	containerHelper := NewContainerHelper(
		env,
		envManager,
		clock.NewMock(),
		containerRegistryService,
		remoteBuildManager,
		dockerCli,
		dotnetCli,
		mockContext.Console,
		cloud.AzurePublic(),
	)

	if userConfig == nil {
		userConfig = config.NewConfig(nil)
	}

	configManager := &mockUserConfigManager{}
	configManager.On("Load").Return(userConfig, nil)

	return NewAksTarget(
		env,
		envManager,
		mockContext.Console,
		managedClustersService,
		resourceManager,
		kubeCtl,
		kubeLoginCli,
		helmCli,
		kustomizeCli,
		containerHelper,
		alpha.NewFeaturesManagerWithConfig(userConfig),
	)
}

func simulateInitliaze(ctx context.Context, serviceTarget ServiceTarget, serviceConfig *ServiceConfig) error {
	if err := serviceTarget.Initialize(ctx, serviceConfig); err != nil {
		return err
	}

	err := serviceConfig.RaiseEvent(ctx, "predeploy", ServiceLifecycleEventArgs{
		Project: serviceConfig.Project,
		Service: serviceConfig,
	})

	if err != nil {
		return err
	}

	return nil
}

func createTestCluster(clusterName, username string) *kubectl.KubeConfig {
	return &kubectl.KubeConfig{
		ApiVersion:     "v1",
		Kind:           "Config",
		CurrentContext: clusterName,
		Preferences:    kubectl.KubePreferences{},
		Clusters: []*kubectl.KubeCluster{
			{
				Name: clusterName,
				Cluster: kubectl.KubeClusterData{
					Server: fmt.Sprintf("https://%s.eastus2.azmk8s.io:443", clusterName),
				},
			},
		},
		Users: []*kubectl.KubeUser{
			{
				Name: fmt.Sprintf("%s_%s", clusterName, username),
			},
		},
		Contexts: []*kubectl.KubeContext{
			{
				Name: clusterName,
				Context: kubectl.KubeContextData{
					Cluster: clusterName,
					User:    fmt.Sprintf("%s_%s", clusterName, username),
				},
			},
		},
	}
}

// logProgress is shorthand for calling async.RunWithProgress a function that calls t.Log with the progress value
// as the observer.
func logProgress[T comparable, P comparable](
	t *testing.T,
	fn func(progess *async.Progress[P]) (T, error),
) (T, error) {
	return async.RunWithProgress(func(p P) { t.Log(p) }, fn)
}

type MockResourceManager struct {
	mock.Mock
}

func (m *MockResourceManager) GetResourceGroupName(
	ctx context.Context,
	subscriptionId string,
	resourceGroupTemplate osutil.ExpandableString,
) (string, error) {
	args := m.Called(ctx, subscriptionId, resourceGroupTemplate)
	return args.String(0), args.Error(1)
}

func (m *MockResourceManager) GetServiceResources(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	serviceConfig *ServiceConfig,
) ([]*azapi.Resource, error) {
	args := m.Called(ctx, subscriptionId, resourceGroupName, serviceConfig)
	return args.Get(0).([]*azapi.Resource), args.Error(1)
}

func (m *MockResourceManager) GetServiceResource(
	ctx context.Context,
	subscriptionId string,
	resourceGroupName string,
	serviceConfig *ServiceConfig,
	rerunCommand string,
) (*azapi.Resource, error) {
	args := m.Called(ctx, subscriptionId, resourceGroupName, serviceConfig, rerunCommand)
	return args.Get(0).(*azapi.Resource), args.Error(1)
}

func (m *MockResourceManager) GetTargetResource(
	ctx context.Context,
	subscriptionId string,
	serviceConfig *ServiceConfig,
) (*environment.TargetResource, error) {
	args := m.Called(ctx, subscriptionId, serviceConfig)
	return args.Get(0).(*environment.TargetResource), args.Error(1)
}

type mockUserConfigManager struct {
	mock.Mock
}

func (m *mockUserConfigManager) Load() (config.Config, error) {
	args := m.Called()
	return args.Get(0).(config.Config), args.Error(1)
}

func (m *mockUserConfigManager) Save(config config.Config) error {
	args := m.Called(config)
	return args.Error(0)
}
