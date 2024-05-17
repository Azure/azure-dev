package appinsightsexporter

import (
	"errors"
	"fmt"
	"strings"
)

const (
	defaultIngestionPrefix = "dc"
	defaultBaseEndpoint    = "https://dc.services.visualstudio.com"
	defaultEndpointPath    = "v2/track"
)

const (
	instrumentationKey_Setting = "InstrumentationKey"
	endpointSuffix_Setting     = "EndpointSuffix"
	ingestionEndpoint_Setting  = "IngestionEndpoint"
)

type EndpointConfig struct {
	EndpointUrl        string
	InstrumentationKey string
}

// NewEndpointConfig parses a connection string, returning the endpoint configuration from the connection string.
//
// The connection string schema for AppInsights can be found at
// https://learn.microsoft.com/azure/azure-monitor/app/sdk-connection-string?tabs=net#schema
func NewEndpointConfig(connectionString string) (EndpointConfig, error) {
	tc := EndpointConfig{}
	settings, err := parseSettings(connectionString)
	if err != nil {
		return tc, err
	}

	var iKey string
	if key, has := settings[instrumentationKey_Setting]; has {
		iKey = key
	} else {
		return tc, fmt.Errorf("%s is missing", instrumentationKey_Setting)
	}

	var baseEndpoint string
	if ingestion, has := settings[ingestionEndpoint_Setting]; has {
		baseEndpoint = ingestion
	} else {
		if endpointSuffix, has := settings[endpointSuffix_Setting]; has {
			endpointSuffix := strings.TrimLeft(endpointSuffix, ".")
			endpointSuffix = strings.TrimRight(endpointSuffix, "/")

			baseEndpoint = fmt.Sprintf("https://%s.%s", defaultIngestionPrefix, endpointSuffix)
		} else {
			baseEndpoint = defaultBaseEndpoint
		}
	}

	tc.InstrumentationKey = iKey
	baseEndpoint = strings.TrimRight(baseEndpoint, "/")
	tc.EndpointUrl = fmt.Sprintf("%s/%s", baseEndpoint, defaultEndpointPath)
	return tc, nil
}

func parseSettings(connectionString string) (map[string]string, error) {
	results := map[string]string{}
	// Split "foo=bar;bar=baz"
	settings := strings.Split(connectionString, ";")

	for _, setting := range settings {
		// Split "foo=bar"
		kvp := strings.Split(setting, "=")
		if len(kvp) != 2 {
			return nil, errors.New("malformed setting. setting should be in the form of 'key=value'")
		}

		if kvp[0] == "" {
			return nil, errors.New("malformed setting. setting key cannot be empty")
		}

		results[kvp[0]] = kvp[1]
	}

	return results, nil
}
