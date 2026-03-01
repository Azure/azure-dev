// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"errors"
	"net/url"
	"strings"
)

// ScopeDetector maps Azure resource endpoint URLs to the OAuth 2.0 scopes
// required for token acquisition. Extensions use this to automatically
// determine the correct scope for a given API call without hard-coding values.
//
// Usage:
//
//	sd := azdext.NewScopeDetector(nil)
//	scopes, err := sd.ScopesForURL("https://management.azure.com/subscriptions/...")
//	// scopes = []string{"https://management.azure.com/.default"}
type ScopeDetector struct {
	rules []scopeRule
}

// scopeRule binds a host-matching function to a scope.
type scopeRule struct {
	match func(host string) bool
	scope string
}

// ScopeDetectorOptions allows adding custom endpoint-to-scope mappings.
type ScopeDetectorOptions struct {
	// CustomRules appends additional host → scope mappings.
	// Each entry maps a host suffix (e.g. ".openai.azure.com") to a scope
	// (e.g. "https://cognitiveservices.azure.com/.default").
	CustomRules map[string]string
}

// defaultRules contains well-known Azure endpoint → scope mappings.
// Order does not matter; rules are evaluated until a match is found.
func defaultRules() []scopeRule {
	suffix := func(s string) func(string) bool {
		return func(host string) bool { return strings.HasSuffix(host, s) }
	}
	exact := func(s string) func(string) bool {
		return func(host string) bool { return host == s }
	}

	return []scopeRule{
		// Azure Resource Manager
		{match: exact("management.azure.com"), scope: "https://management.azure.com/.default"},

		// Microsoft Graph
		{match: exact("graph.microsoft.com"), scope: "https://graph.microsoft.com/.default"},

		// Azure Key Vault
		{match: suffix(".vault.azure.net"), scope: "https://vault.azure.net/.default"},

		// Azure Storage (Blob, Queue, Table, File, Data Lake)
		{match: suffix(".blob.core.windows.net"), scope: "https://storage.azure.com/.default"},
		{match: suffix(".queue.core.windows.net"), scope: "https://storage.azure.com/.default"},
		{match: suffix(".table.core.windows.net"), scope: "https://storage.azure.com/.default"},
		{match: suffix(".file.core.windows.net"), scope: "https://storage.azure.com/.default"},
		{match: suffix(".dfs.core.windows.net"), scope: "https://storage.azure.com/.default"},

		// Azure Container Registry
		{match: suffix(".azurecr.io"), scope: "https://management.azure.com/.default"},

		// Azure Cognitive Services / OpenAI
		{match: suffix(".openai.azure.com"), scope: "https://cognitiveservices.azure.com/.default"},
		{match: suffix(".cognitiveservices.azure.com"), scope: "https://cognitiveservices.azure.com/.default"},

		// Azure AI Services
		{match: suffix(".services.ai.azure.com"), scope: "https://cognitiveservices.azure.com/.default"},

		// Azure DevOps
		{match: exact("dev.azure.com"), scope: "499b84ac-1321-427f-aa17-267ca6975798/.default"},
		{match: suffix(".visualstudio.com"), scope: "499b84ac-1321-427f-aa17-267ca6975798/.default"},

		// Azure Database for PostgreSQL
		{match: suffix(".postgres.database.azure.com"), scope: "https://ossrdbms-aad.database.windows.net/.default"},

		// Azure Database for MySQL
		{match: suffix(".mysql.database.azure.com"), scope: "https://ossrdbms-aad.database.windows.net/.default"},

		// Azure Cosmos DB
		{match: suffix(".documents.azure.com"), scope: "https://cosmos.azure.com/.default"},

		// Azure Event Hubs
		{match: suffix(".servicebus.windows.net"), scope: "https://eventhubs.azure.net/.default"},

		// Azure App Configuration
		{match: suffix(".azconfig.io"), scope: "https://azconfig.io/.default"},
	}
}

// NewScopeDetector creates a [ScopeDetector] with the built-in Azure endpoint
// mappings. Additional custom rules can be supplied via opts.
func NewScopeDetector(opts *ScopeDetectorOptions) *ScopeDetector {
	rules := defaultRules()

	if opts != nil {
		for hostSuffix, scope := range opts.CustomRules {
			hs := hostSuffix // capture
			rules = append(rules, scopeRule{
				match: func(host string) bool { return strings.HasSuffix(host, hs) },
				scope: scope,
			})
		}
	}

	return &ScopeDetector{rules: rules}
}

// ScopesForURL returns the OAuth 2.0 scopes required to access the given URL.
// Returns an error if the URL is malformed or no matching scope is found.
func (sd *ScopeDetector) ScopesForURL(rawURL string) ([]string, error) {
	if rawURL == "" {
		return nil, errors.New("azdext.ScopeDetector.ScopesForURL: URL must not be empty")
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, &ScopeDetectorError{URL: rawURL, Reason: "malformed URL: " + err.Error()}
	}

	host := strings.ToLower(u.Hostname())
	if host == "" {
		return nil, &ScopeDetectorError{URL: rawURL, Reason: "URL has no host"}
	}

	for _, rule := range sd.rules {
		if rule.match(host) {
			return []string{rule.scope}, nil
		}
	}

	return nil, &ScopeDetectorError{URL: rawURL, Reason: "no scope mapping found for host: " + host}
}

// ScopeDetectorError is returned when [ScopeDetector.ScopesForURL] cannot
// resolve a scope for the given URL.
type ScopeDetectorError struct {
	URL    string
	Reason string
}

func (e *ScopeDetectorError) Error() string {
	return "azdext.ScopeDetector: " + e.Reason + " (url=" + e.URL + ")"
}
