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

// TODO: We might just be able to get away with cloud.Configuration here
type Cloud struct {
	// TODO: Should this be a pointer? Yes depending on where during runtime
	// the Services values are set.
	// Probably not necessary... cloud configurations are initialized in init()
	// so they should be stable by the time application logic is called. Plus
	// maps are reference types so passing cloud configuration is relatively
	// inexpensive.

	// TODO: pass by value, remove pointer
	Configuration *cloud.Configuration

	// The base URL for the cloud's portal (e.g. https://portal.azure.com for
	// Azure public cloud).
	// TODO: How would this configuration be configured by a user?
	PortalUrlBase string

	// The base URL for cloud's Azure Websites (e.g. azurewebsites.net for Azure
	// public cloud).
	// TODO: Resolve using ARM and look at .hostNameSslStates where
	// hostType == 1 (i.e. "repository") or hosts with ".scm."?
	WebSitesUrlBase string
}

type Config struct {
	Name string `json:"name,omitempty" yaml:"name,omitempty"`
}

func NewCloud(config *Config) *Cloud {
	if cloud, err := getNamedCloud(config.Name); err != nil {
		// panic here on invalid config?
		publicCloud := GetAzurePublic()
		return &publicCloud
	} else {
		return &cloud
	}
}

// parseConfig attempts to parse a partial JSON configuration into a cloud configuration
// TODO: Can this be generalized to deduplicate the same function in devcenter.go?
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

func GetAzurePublic() Cloud {
	return Cloud{
		Configuration:   &cloud.AzurePublic,
		PortalUrlBase:   "https://portal.azure.com",
		WebSitesUrlBase: "azurewebsites.net",
	}
}

func GetAzureGovernment() Cloud {
	return Cloud{
		Configuration:   &cloud.AzureGovernment,
		PortalUrlBase:   "https://portal.azure.us",
		WebSitesUrlBase: "azurewebsites.us",
	}
}

func GetAzureChina() Cloud {
	return Cloud{
		Configuration: &cloud.AzureChina,
		PortalUrlBase: "https://portal.azure.cn",
		// TODO: fill in WebSitesUrlBase
	}
}

func getNamedCloud(name string) (Cloud, error) {
	if name == AzurePublicName || name == "" {
		return GetAzurePublic(), nil
	} else if name == AzureChinaCloudName {
		return GetAzureChina(), nil
	} else if name == AzureUSGovernmentName {
		return GetAzureGovernment(), nil
	}

	return Cloud{}, fmt.Errorf("cloud '%s' not found", name)
}
