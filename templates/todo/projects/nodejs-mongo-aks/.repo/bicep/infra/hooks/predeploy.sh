#!/bin/bash

echo "Retrieving cluster credentials"
az aks get-credentials --resource-group ${AZURE_RESOURCE_GROUP} --name ${AZURE_AKS_CLUSTER_NAME}

appConfigProviderStatus=$(helm status azureappconfiguration.kubernetesprovider -n azappconfig-system)

if [ $? -eq 0 ]; then
  echo "Azure App configuration Kubernetes provider installed, skipping"
else
  echo "Azure App configuration Kubernetes provider not installed, installing"
  helm install azureappconfiguration.kubernetesprovider oci://mcr.microsoft.com/azure-app-configuration/helmchart/kubernetes-provider --namespace azappconfig-system --create-namespace
fi