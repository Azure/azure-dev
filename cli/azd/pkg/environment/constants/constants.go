// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

// Package constants defines environment variable names without importing environment management dependencies.
package constants

// AksClusterEnvVarName is the name of the key used to store the AKS cluster name.
const AksClusterEnvVarName = "AZURE_AKS_CLUSTER_NAME"

// AppServiceAspireDashboardUrlEnvVarName is the name of the key used to store the App Service Aspire Dashboard URL.
const AppServiceAspireDashboardUrlEnvVarName = "AZURE_APP_SERVICE_DASHBOARD_URI"

// AzdInitialEnvironmentConfigName is the name of the key used to initialize environment configuration in CI/CD.
// This variable is deprecated in favor of individual pipeline variables and secrets.
const AzdInitialEnvironmentConfigName = "AZD_INITIAL_ENVIRONMENT_CONFIG"

// ContainerEnvironmentEndpointEnvVarName is the name of the key used to store the Container Apps environment default domain.
const ContainerEnvironmentEndpointEnvVarName = "AZURE_CONTAINER_APPS_ENVIRONMENT_DEFAULT_DOMAIN"

// ContainerRegistryEndpointEnvVarName is the name of the key used to store the container registry endpoint.
const ContainerRegistryEndpointEnvVarName = "AZURE_CONTAINER_REGISTRY_ENDPOINT"

// EnvNameEnvVarName is the name of the key used to store the environment name.
const EnvNameEnvVarName = "AZURE_ENV_NAME"

// LocationEnvVarName is the name of the key used to store the Azure location.
const LocationEnvVarName = "AZURE_LOCATION"

// PlatformTypeEnvVarName is the name of the key used to store the azd platform type.
const PlatformTypeEnvVarName = "AZD_PLATFORM_TYPE"

// PrincipalIdEnvVarName is the name of the key used to store the principal ID.
const PrincipalIdEnvVarName = "AZURE_PRINCIPAL_ID"

// PrincipalTypeEnvVarName is the name of the key used to store the principal type.
const PrincipalTypeEnvVarName = "AZURE_PRINCIPAL_TYPE"

// ResourceGroupEnvVarName is the name of the key used to store the deployment resource group.
const ResourceGroupEnvVarName = "AZURE_RESOURCE_GROUP"

// SubscriptionIdEnvVarName is the name of the key used to store the Azure subscription ID.
const SubscriptionIdEnvVarName = "AZURE_SUBSCRIPTION_ID"

// TenantIdEnvVarName is the name of the key used to store the Azure tenant ID.
const TenantIdEnvVarName = "AZURE_TENANT_ID"
