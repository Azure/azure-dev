package project

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerregistry/armcontainerregistry"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice/v2"
	"github.com/azure/azure-dev/cli/azd/pkg/async"
	"github.com/azure/azure-dev/cli/azd/pkg/convert"
	"github.com/azure/azure-dev/cli/azd/pkg/environment"
	"github.com/azure/azure-dev/cli/azd/pkg/exec"
	"github.com/azure/azure-dev/cli/azd/pkg/infra"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/azcli"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/docker"
	"github.com/azure/azure-dev/cli/azd/pkg/tools/kubectl"
	"github.com/azure/azure-dev/cli/azd/test/mocks"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockaccount"
	"github.com/azure/azure-dev/cli/azd/test/ostest"
	"github.com/benbjohnson/clock"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func Test_NewAksTarget(t *testing.T) {
	mockContext := mocks.NewMockContext(context.Background())
	serviceConfig := createTestServiceConfig("./src/api", AksTarget, ServiceLanguageTypeScript)
	env := createEnv()

	serviceTarget := createServiceTarget(mockContext, serviceConfig, env)

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

	serviceTarget := createServiceTarget(mockContext, serviceConfig, env)

	requiredTools := serviceTarget.RequiredExternalTools(*mockContext.Context)
	require.Len(t, requiredTools, 2)
	require.Implements(t, new(docker.Docker), requiredTools[0])
	require.Implements(t, new(kubectl.KubectlCli), requiredTools[1])
}

func Test_Package_Publish_HappyPath(t *testing.T) {
	tempDir := t.TempDir()
	ostest.Chdir(t, tempDir)

	mockContext := mocks.NewMockContext(context.Background())
	err := setupMocksForAksTarget(mockContext)
	require.NoError(t, err)

	serviceConfig := createTestServiceConfig(tempDir, AksTarget, ServiceLanguageTypeScript)
	env := createEnv()

	serviceTarget := createServiceTarget(mockContext, serviceConfig, env)
	err = setupK8sManifests(t, serviceConfig)
	require.NoError(t, err)

	packageTask := serviceTarget.Package(
		*mockContext.Context,
		serviceConfig,
		&ServicePackageResult{
			PackagePath: "test-app/api-test:azd-deploy-0",
			Details: &dockerPackageResult{
				ImageHash: "IMAGE_HASH",
				ImageTag:  "test-app/api-test:azd-deploy-0",
			},
		},
	)
	logProgress(packageTask)
	packageResult, err := packageTask.Await()

	require.NoError(t, err)
	require.NotNil(t, packageResult)
	require.IsType(t, new(dockerPackageResult), packageResult.Details)

	scope := environment.NewTargetResource("SUB_ID", "RG_ID", "CLUSTER_NAME", string(infra.AzureResourceTypeManagedCluster))
	publishTask := serviceTarget.Publish(*mockContext.Context, serviceConfig, packageResult, scope)
	logProgress(publishTask)
	publishResult, err := publishTask.Await()

	require.NoError(t, err)
	require.NotNil(t, publishResult)
	require.Equal(t, AksTarget, publishResult.Kind)
	require.IsType(t, new(kubectl.Deployment), publishResult.Details)
	require.Greater(t, len(publishResult.Endpoints), 0)
	// New env variable is created
	require.Equal(t, "REGISTRY.azurecr.io/test-app/api-test:azd-deploy-0", env.Values["SERVICE_API_IMAGE_NAME"])
}

func Test_Publish_No_Cluster_Name(t *testing.T) {
	tempDir := t.TempDir()
	ostest.Chdir(t, tempDir)

	mockContext := mocks.NewMockContext(context.Background())
	err := setupMocksForAksTarget(mockContext)
	require.NoError(t, err)

	serviceConfig := createTestServiceConfig(tempDir, AksTarget, ServiceLanguageTypeScript)
	env := createEnv()

	// Simulate AKS cluster name not found in env file
	delete(env.Values, environment.AksClusterEnvVarName)

	serviceTarget := createServiceTarget(mockContext, serviceConfig, env)
	scope := environment.NewTargetResource("SUB_ID", "RG_ID", "CLUSTER_NAME", string(infra.AzureResourceTypeManagedCluster))
	packageOutput := &ServicePackageResult{
		Build: &ServiceBuildResult{BuildOutputPath: "IMAGE_ID"},
		Details: &dockerPackageResult{
			ImageTag: "IMAGE_TAG",
		},
	}

	publishTask := serviceTarget.Publish(*mockContext.Context, serviceConfig, packageOutput, scope)
	logProgress(publishTask)

	publishResult, err := publishTask.Await()
	require.Error(t, err)
	require.ErrorContains(t, err, "could not determine AKS cluster")
	require.Nil(t, publishResult)
}

func Test_Publish_No_Admin_Credentials(t *testing.T) {
	tempDir := t.TempDir()
	ostest.Chdir(t, tempDir)

	mockContext := mocks.NewMockContext(context.Background())
	err := setupMocksForAksTarget(mockContext)
	require.NoError(t, err)

	// Simulate list credentials fail.
	// For more secure clusters getting admin credentials can fail
	err = setupListClusterAdminCredentialsMock(mockContext, http.StatusUnauthorized)
	require.NoError(t, err)

	serviceConfig := createTestServiceConfig(tempDir, AksTarget, ServiceLanguageTypeScript)
	env := createEnv()

	serviceTarget := createServiceTarget(mockContext, serviceConfig, env)
	scope := environment.NewTargetResource("SUB_ID", "RG_ID", "CLUSTER_NAME", string(infra.AzureResourceTypeManagedCluster))
	packageOutput := &ServicePackageResult{
		Build: &ServiceBuildResult{BuildOutputPath: "IMAGE_ID"},
		Details: &dockerPackageResult{
			ImageTag: "IMAGE_TAG",
		},
	}

	publishTask := serviceTarget.Publish(*mockContext.Context, serviceConfig, packageOutput, scope)
	logProgress(publishTask)
	publishResult, err := publishTask.Await()

	require.Error(t, err)
	require.ErrorContains(t, err, "failed retrieving cluster admin credentials")
	require.Nil(t, publishResult)
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
					Name:  convert.RefOf("context"),
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

func setupMocksForAksTarget(mockContext *mocks.MockContext) error {
	err := setupListClusterAdminCredentialsMock(mockContext, http.StatusOK)
	if err != nil {
		return err
	}

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

	// Apply Pipe
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "kubectl apply -f -")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "", ""), nil
	})

	// Create Secret
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "kubectl create secret generic")
	}).RespondFn(func(args exec.RunArgs) (exec.RunResult, error) {
		return exec.NewRunResult(0, "", ""), nil
	})

	// List container registries
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet &&
			strings.Contains(request.URL.Path, "Microsoft.ContainerRegistry/registries")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		result := armcontainerregistry.RegistryListResult{
			NextLink: nil,
			Value: []*armcontainerregistry.Registry{
				{
					ID: convert.RefOf(
						//nolint:lll
						"/subscriptions/SUBSCRIPTION_ID/resourceGroups/RESOURCE_GROUP/providers/Microsoft.ContainerRegistry/registries/REGISTRY",
					),
					Location: convert.RefOf("eastus2"),
					Name:     convert.RefOf("REGISTRY"),
					Properties: &armcontainerregistry.RegistryProperties{
						LoginServer: convert.RefOf("REGISTRY.azurecr.io"),
					},
				},
			},
		}

		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, result)
	})

	// List container credentials
	mockContext.HttpClient.When(func(request *http.Request) bool {
		return request.Method == http.MethodPost && strings.Contains(request.URL.Path, "listCredentials")
	}).RespondFn(func(request *http.Request) (*http.Response, error) {
		result := armcontainerregistry.RegistryListCredentialsResult{
			Username: convert.RefOf("admin"),
			Passwords: []*armcontainerregistry.RegistryPassword{
				{
					Name:  convert.RefOf(armcontainerregistry.PasswordName("admin")),
					Value: convert.RefOf("password"),
				},
			},
		}

		return mocks.CreateHttpResponseWithBody(request, http.StatusOK, result)
	})

	// Docker login
	mockContext.CommandRunner.When(func(args exec.RunArgs, command string) bool {
		return strings.Contains(command, "docker login")
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

	return nil
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
	return environment.EphemeralWithValues("test", map[string]string{
		environment.TenantIdEnvVarName:                  "TENANT_ID",
		environment.SubscriptionIdEnvVarName:            "SUBSCRIPTION_ID",
		environment.LocationEnvVarName:                  "LOCATION",
		environment.ResourceGroupEnvVarName:             "RESOURCE_GROUP",
		environment.AksClusterEnvVarName:                "AKS_CLUSTER",
		environment.ContainerRegistryEndpointEnvVarName: "REGISTRY.azurecr.io",
	})
}

func createServiceTarget(
	mockContext *mocks.MockContext,
	serviceConfig *ServiceConfig,
	env *environment.Environment,
) ServiceTarget {
	kubeCtl := kubectl.NewKubectl(mockContext.CommandRunner)
	dockerCli := docker.NewDocker(mockContext.CommandRunner)
	credentialProvider := mockaccount.SubscriptionCredentialProviderFunc(
		func(_ context.Context, _ string) (azcore.TokenCredential, error) {
			return mockContext.Credentials, nil
		})

	managedClustersService := azcli.NewManagedClustersService(credentialProvider, mockContext.HttpClient)
	containerRegistryService := azcli.NewContainerRegistryService(credentialProvider, mockContext.HttpClient, dockerCli)
	containerHelper := NewContainerHelper(env, clock.NewMock())

	return NewAksTarget(
		env,
		managedClustersService,
		containerRegistryService,
		kubeCtl,
		dockerCli,
		containerHelper,
	)
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

func logProgress[T comparable, P comparable](task *async.TaskWithProgress[T, P]) {
	go func() {
		for value := range task.Progress() {
			log.Println(value)
		}
	}()
}
