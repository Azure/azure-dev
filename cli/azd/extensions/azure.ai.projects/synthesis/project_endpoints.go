// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package synthesis

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"strings"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/arm"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices/v2"
)

const foundryProjectHostSuffix = ".services.ai.azure.com"

var foundrySubdomainPattern = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)

// FoundryProjectMetadata binds data-plane endpoints to ARM resources.
type FoundryProjectMetadata struct {
	Project        armcognitiveservices.Project
	Account        armcognitiveservices.Account
	Endpoint       string
	OpenAIEndpoint string
}

// LookupFoundryProject reads ARM project and account metadata.
// Only missing metadata permits compatibility URLs.
func LookupFoundryProject(
	ctx context.Context,
	id *arm.ResourceID,
	credential azcore.TokenCredential,
	options *arm.ClientOptions,
) (*FoundryProjectMetadata, error) {
	if id == nil || id.Parent == nil || id.SubscriptionID == "" || id.ResourceGroupName == "" ||
		!strings.EqualFold(id.ResourceType.String(), "Microsoft.CognitiveServices/accounts/projects") {
		return nil, fmt.Errorf("a Foundry project ARM resource ID is required")
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
	if p.Account.Properties != nil && p.Account.Properties.CustomSubDomainName != nil &&
		*p.Account.Properties.CustomSubDomainName != "" {
		subdomain = *p.Account.Properties.CustomSubDomainName
		if !foundrySubdomainPattern.MatchString(subdomain) {
			return fmt.Errorf("Foundry account metadata contains an invalid custom subdomain")
		}
	}
	endpoint := fmt.Sprintf("https://%s%s/api/projects/%s", subdomain, foundryProjectHostSuffix, projectName)
	if p.Project.Properties != nil {
		if advertised := p.Project.Properties.Endpoints["AI Foundry API"]; advertised != nil && *advertised != "" {
			endpoint = *advertised
		}
	}
	normalized, err := ValidateFoundryProjectEndpoint(endpoint)
	if err != nil {
		return fmt.Errorf("invalid ARM-advertised Foundry project endpoint: %w", err)
	}
	parsed, _ := url.Parse(normalized)
	if !strings.EqualFold(strings.TrimPrefix(strings.TrimPrefix(parsed.Path, "/api"), "/projects/"), projectName) {
		return fmt.Errorf("ARM-advertised Foundry endpoint identifies a different project")
	}
	p.Endpoint = normalized
	if p.Account.Properties == nil || p.Account.Properties.CustomSubDomainName == nil ||
		*p.Account.Properties.CustomSubDomainName == "" {
		subdomain = strings.TrimSuffix(parsed.Hostname(), foundryProjectHostSuffix)
	}
	p.OpenAIEndpoint = fmt.Sprintf("https://%s.openai.azure.com/", strings.ToLower(subdomain))
	return nil
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
	path := strings.TrimPrefix(strings.TrimRight(parsed.Path, "/"), "/api")
	project := strings.TrimPrefix(path, "/projects/")
	if project == path || project == "" || project == "." || project == ".." ||
		strings.ContainsAny(project, `/\?#`) {
		return "", fmt.Errorf("Foundry project endpoint must have the path /api/projects/<project>")
	}
	return "https://" + strings.ToLower(parsed.Hostname()) + strings.TrimRight(parsed.EscapedPath(), "/"), nil
}
