// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package synthesis

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices/v2"
)

const foundryProjectHostSuffix = ".services.ai.azure.com"

var foundrySubdomainPattern = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)

var errMissingFoundryProjectAdvertisedEndpoint = errors.New("missing ARM-advertised Foundry project endpoint")

// FoundryProjectMetadataValidationError reports malformed or conflicting ARM metadata.
type FoundryProjectMetadataValidationError struct {
	message string
}

func (e *FoundryProjectMetadataValidationError) Error() string {
	return e.message
}

func metadataValidationErrorf(format string, args ...any) error {
	return &FoundryProjectMetadataValidationError{message: fmt.Sprintf(format, args...)}
}

// IsFoundryProjectMetadataValidationError reports whether err is metadata validation failure.
func IsFoundryProjectMetadataValidationError(err error) bool {
	_, ok := errors.AsType[*FoundryProjectMetadataValidationError](err)
	return ok
}

// FoundryProjectMetadata binds data-plane endpoints to ARM resources.
type FoundryProjectMetadata struct {
	Project        armcognitiveservices.Project
	Account        armcognitiveservices.Account
	Endpoint       string
	OpenAIEndpoint string
}

// LookupFoundryProject reads ARM project and account metadata.
// Missing account metadata still permits account-subdomain/account-name
// compatibility URL fallback. HTTP 403 from account lookup is tolerated only
// when the project advertises a valid matching ARM endpoint.
func LookupFoundryProject(
	ctx context.Context,
	id *arm.ResourceID,
	credential azcore.TokenCredential,
	options *arm.ClientOptions,
) (*FoundryProjectMetadata, error) {
	if id == nil || id.Parent == nil || id.SubscriptionID == "" || id.ResourceGroupName == "" ||
		!strings.EqualFold(id.ResourceType.String(), "Microsoft.CognitiveServices/accounts/projects") {
		return nil, metadataValidationErrorf("a Foundry project ARM resource ID is required")
	}
	factory, err := armcognitiveservices.NewClientFactory(id.SubscriptionID, credential, options)
	if err != nil {
		return nil, fmt.Errorf("create Foundry ARM clients: %w", err)
	}
	project, err := factory.NewProjectsClient().Get(ctx, id.ResourceGroupName, id.Parent.Name, id.Name, nil)
	if err != nil {
		return nil, fmt.Errorf("read Foundry project: %w", err)
	}
	account, err := factory.NewAccountsClient().Get(ctx, id.ResourceGroupName, id.Parent.Name, nil)
	if err != nil {
		if responseErr, ok := errors.AsType[*azcore.ResponseError](err); ok &&
			responseErr.StatusCode == http.StatusForbidden {
			result := &FoundryProjectMetadata{Project: project.Project}
			if fallbackErr := result.resolveProjectAdvertisedEndpoints(id.Name); fallbackErr == nil {
				return result, nil
			} else if !errors.Is(fallbackErr, errMissingFoundryProjectAdvertisedEndpoint) {
				return nil, fallbackErr
			}
		}
		return nil, fmt.Errorf("read Foundry account: %w", err)
	}
	result := &FoundryProjectMetadata{Project: project.Project, Account: account.Account}
	if err := result.resolveEndpoints(id.Parent.Name, id.Name); err != nil {
		return nil, err
	}
	return result, nil
}

func (p *FoundryProjectMetadata) resolveEndpoints(accountName, projectName string) error {
	subdomain := accountName
	customSubdomain := ""
	if p.Account.Properties != nil && p.Account.Properties.CustomSubDomainName != nil &&
		*p.Account.Properties.CustomSubDomainName != "" {
		customSubdomain = *p.Account.Properties.CustomSubDomainName
		if !foundrySubdomainPattern.MatchString(customSubdomain) {
			return metadataValidationErrorf("Foundry account metadata contains an invalid custom subdomain")
		}
		subdomain = customSubdomain
	}
	endpoint := fmt.Sprintf("https://%s%s/api/projects/%s", subdomain, foundryProjectHostSuffix, projectName)
	if p.Project.Properties != nil {
		if advertised := p.Project.Properties.Endpoints["AI Foundry API"]; advertised != nil && *advertised != "" {
			endpoint = *advertised
		}
	}
	normalized, endpointSubdomain, err := normalizeProjectEndpoint(endpoint, projectName)
	if err != nil {
		return err
	}
	if customSubdomain != "" && !strings.EqualFold(endpointSubdomain, customSubdomain) {
		return metadataValidationErrorf("ARM-advertised Foundry endpoint does not match account custom subdomain")
	}
	p.Endpoint = normalized
	if customSubdomain == "" {
		subdomain = endpointSubdomain
	}
	p.OpenAIEndpoint = fmt.Sprintf("https://%s.openai.azure.com/", strings.ToLower(subdomain))
	return nil
}

func (p *FoundryProjectMetadata) resolveProjectAdvertisedEndpoints(projectName string) error {
	if p.Project.Properties == nil {
		return errMissingFoundryProjectAdvertisedEndpoint
	}
	advertised := p.Project.Properties.Endpoints["AI Foundry API"]
	if advertised == nil || strings.TrimSpace(*advertised) == "" {
		return errMissingFoundryProjectAdvertisedEndpoint
	}
	normalized, endpointSubdomain, err := normalizeProjectEndpoint(*advertised, projectName)
	if err != nil {
		return err
	}
	p.Endpoint = normalized
	p.OpenAIEndpoint = fmt.Sprintf("https://%s.openai.azure.com/", strings.ToLower(endpointSubdomain))
	return nil
}

func normalizeProjectEndpoint(endpoint, projectName string) (string, string, error) {
	normalized, err := ValidateFoundryProjectEndpoint(endpoint)
	if err != nil {
		return "", "", metadataValidationErrorf("invalid ARM-advertised Foundry project endpoint: %s", err)
	}
	parsed, _ := url.Parse(normalized)
	path := strings.TrimPrefix(strings.TrimRight(parsed.Path, "/"), "/")
	segments := strings.Split(path, "/")
	matchesAPIPath := len(segments) == 3 &&
		strings.EqualFold(segments[0], "api") &&
		strings.EqualFold(segments[1], "projects") &&
		strings.EqualFold(segments[2], projectName)
	matchesLegacyPath := len(segments) == 2 &&
		strings.EqualFold(segments[0], "projects") &&
		strings.EqualFold(segments[1], projectName)
	if !matchesAPIPath && !matchesLegacyPath {
		return "", "", metadataValidationErrorf("ARM-advertised Foundry endpoint identifies a different project")
	}
	return normalized, strings.TrimSuffix(parsed.Hostname(), foundryProjectHostSuffix), nil
}

// ValidateFoundryProjectEndpoint normalizes an Azure project URL.
// It rejects credentials and non-Azure origins before authentication.
func ValidateFoundryProjectEndpoint(endpoint string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(endpoint))
	if err != nil {
		return "", fmt.Errorf("invalid Foundry project endpoint URL")
	}
	if !strings.EqualFold(parsed.Scheme, "https") || parsed.Hostname() == "" || parsed.Host != parsed.Hostname() ||
		!strings.HasSuffix(strings.ToLower(parsed.Hostname()), foundryProjectHostSuffix) {
		return "", fmt.Errorf("Foundry project endpoint must use https://<subdomain>.services.ai.azure.com")
	}
	subdomain := strings.TrimSuffix(strings.ToLower(parsed.Hostname()), foundryProjectHostSuffix)
	if !foundrySubdomainPattern.MatchString(subdomain) {
		return "", fmt.Errorf("Foundry project endpoint has an invalid Azure subdomain")
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.ForceQuery || parsed.Fragment != "" {
		return "", fmt.Errorf("Foundry project endpoint must not contain credentials, a query, or a fragment")
	}
	path := strings.TrimRight(parsed.Path, "/")
	segments := strings.Split(path, "/")
	useAPIPath := len(segments) == 4 &&
		segments[0] == "" &&
		strings.EqualFold(segments[1], "api") &&
		strings.EqualFold(segments[2], "projects")
	useLegacyPath := len(segments) == 3 &&
		segments[0] == "" &&
		strings.EqualFold(segments[1], "projects")
	project := ""
	switch {
	case useAPIPath:
		project = segments[3]
	case useLegacyPath:
		project = segments[2]
	default:
		return "", fmt.Errorf("Foundry project endpoint must have the path /api/projects/<project> or /projects/<project>")
	}
	if project == "" || project == "." || project == ".." ||
		strings.ContainsAny(project, `/\?#`) {
		return "", fmt.Errorf("Foundry project endpoint must have the path /api/projects/<project> or /projects/<project>")
	}
	escapedProject := (&url.URL{Path: project}).EscapedPath()
	if useAPIPath {
		return "https://" + strings.ToLower(parsed.Hostname()) + "/api/projects/" + escapedProject, nil
	}
	return "https://" + strings.ToLower(parsed.Hostname()) + "/projects/" + escapedProject, nil
}
