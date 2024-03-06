package cloud

import (
	"encoding/json"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/cloud"
)

const (
	ConfigPath = "cloud"

	AzurePublicName       = "AzureCloud"
	AzureChinaCloudName   = "AzureChinaCloud"
	AzureUSGovernmentName = "AzureUSGovernment"
)

type PortalUrlBase = string

type Cloud struct {
	Configuration cloud.Configuration

	// The base URL for the cloud's portal (e.g. https://portal.azure.com for
	// Azure public cloud).
	PortalUrlBase string

	// The suffix for the cloud's storage endpoints (e.g. core.windows.net for
	// Azure public cloud). These are well known values and can be found at:
	// https://<management-endpoint>/metadata/endpoints?api-version=2023-12-01
	StorageEndpointSuffix string

	// The suffix for the cloud's container registry endpoints. These are well
	// known values and can be found at:
	// https://<management-endpoint>/metadata/endpoints?api-version=2023-12-01
	ContainerRegistryEndpointSuffix string
}

type Config struct {
	Name string `json:"name,omitempty" yaml:"name,omitempty"`
}

func NewCloud(config *Config) (*Cloud, error) {
	if cloud, err := parseCloudName(config.Name); err != nil {
		return nil, err
	} else {
		return cloud, nil
	}
}

func ParseCloudConfig(partialConfig any) (*Config, error) {
	var config *Config

	jsonBytes, err := json.Marshal(partialConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal cloud configuration: %w", err)
	}

	if err := json.Unmarshal(jsonBytes, &config); err != nil {
		return nil, fmt.Errorf("failed to unmarshal cloud configuration: %w", err)
	}

	return config, nil
}

func AzurePublic() *Cloud {
	return &Cloud{
		Configuration:                   cloud.AzurePublic,
		PortalUrlBase:                   "https://portal.azure.com",
		StorageEndpointSuffix:           "core.windows.net",
		ContainerRegistryEndpointSuffix: "azurecr.io",
	}
}

func AzureGovernment() *Cloud {
	return &Cloud{
		Configuration:                   cloud.AzureGovernment,
		PortalUrlBase:                   "https://portal.azure.us",
		StorageEndpointSuffix:           "core.usgovcloudapi.net",
		ContainerRegistryEndpointSuffix: "azurecr.us",
	}
}

func AzureChina() *Cloud {
	return &Cloud{
		Configuration:                   cloud.AzureChina,
		PortalUrlBase:                   "https://portal.azure.cn",
		StorageEndpointSuffix:           "core.chinacloudapi.cn",
		ContainerRegistryEndpointSuffix: "azurecr.cn",
	}
}

func parseCloudName(name string) (*Cloud, error) {
	if name == AzurePublicName || name == "" {
		return AzurePublic(), nil
	} else if name == AzureChinaCloudName {
		return AzureChina(), nil
	} else if name == AzureUSGovernmentName {
		return AzureGovernment(), nil
	}

	return &Cloud{}, fmt.Errorf("Cloud name '%s' not found.", name)
}
