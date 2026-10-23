// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cuclient

import (
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	client "azure.ai.content-understanding/cu-rest-client"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
)

const cognitiveServicesScope = "https://cognitiveservices.azure.com/.default"

var resourceEndpointHostPattern = regexp.MustCompile(
	`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?\.(?:services\.ai\.azure\.com|cognitiveservices\.azure\.com)$`,
)

type TransportOptions struct {
	APIVersion string
	Version    string
	// RequestTimeout bounds each attempt, including reading its response body.
	RequestTimeout time.Duration
	// Transport is an optional trusted transport for tests or repository-approved networking.
	// An injected transport controls credential delivery and permits custom endpoint hosts and ports.
	Transport policy.Transporter
}

// NormalizeEndpoint validates and normalizes an Azure resource endpoint on HTTPS port 443.
func NormalizeEndpoint(endpoint string) (string, error) {
	return normalizeEndpoint(endpoint, false)
}

func normalizeEndpoint(endpoint string, trustedTransport bool) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || parsed.User != nil ||
		parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" || parsed.RawPath != "" {
		return "", validationError("invalid_endpoint", "Specify an absolute HTTPS CU endpoint without credentials or query.")
	}
	if parsed.Path != "" && parsed.Path != "/" && parsed.Path != "/contentunderstanding" &&
		parsed.Path != "/contentunderstanding/" {
		return "", validationError("invalid_endpoint", "The CU endpoint must be a resource URL, not an operation URL.")
	}
	if port := parsed.Port(); port != "" {
		number, err := strconv.Atoi(port)
		if err != nil || number < 1 || number > 65535 {
			return "", validationError("invalid_endpoint", "The CU endpoint contains an invalid port.")
		}
	}
	if !trustedTransport && (!resourceEndpointHostPattern.MatchString(strings.ToLower(parsed.Hostname())) ||
		(parsed.Port() != "" && parsed.Port() != "443") || strings.HasSuffix(parsed.Host, ":")) {
		return "", validationError("invalid_endpoint",
			"Use an HTTPS Azure resource endpoint at <resource>.services.ai.azure.com "+
				"or <resource>.cognitiveservices.azure.com on port 443.")
	}
	parsed.Path = "/contentunderstanding"
	parsed.Host = strings.ToLower(parsed.Host)
	return parsed.String(), nil
}

// NormalizeAPIVersion applies the default and validates GA or date-formatted preview versions.
func NormalizeAPIVersion(version string) (string, error) {
	value, err := client.NormalizeAPIVersion(version)
	if err != nil {
		return "", validationError("unsupported_api_version",
			"Select 2025-11-01 or a valid YYYY-MM-DD-preview API version.")
	}
	return value, nil
}

func normalizeOptions(options *TransportOptions) (TransportOptions, error) {
	var result TransportOptions
	if options != nil {
		result = *options
	}
	var err error
	if result.APIVersion, err = NormalizeAPIVersion(result.APIVersion); err != nil {
		return result, err
	}
	if result.RequestTimeout < 0 {
		return result, validationError("invalid_request_timeout", "The request timeout must not be negative.")
	}
	if result.RequestTimeout == 0 {
		result.RequestTimeout = 2 * time.Minute
	}
	if result.Version == "" {
		result.Version = "dev"
	}
	if !serviceCodePattern.MatchString(result.Version) {
		return result, validationError("invalid_client_version", "The client version must be a short version identifier.")
	}
	return result, nil
}
