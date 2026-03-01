// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azdext

import (
	"errors"
	"testing"
)

func TestScopeDetector_KnownEndpoints(t *testing.T) {
	t.Parallel()

	sd := NewScopeDetector(nil)

	tests := []struct {
		name      string
		url       string
		wantScope string
	}{
		// ARM
		{
			name:      "ARM subscription list",
			url:       "https://management.azure.com/subscriptions?api-version=2022-01-01",
			wantScope: "https://management.azure.com/.default",
		},
		// Graph
		{
			name:      "Graph users",
			url:       "https://graph.microsoft.com/v1.0/me",
			wantScope: "https://graph.microsoft.com/.default",
		},
		// Key Vault
		{
			name:      "Key Vault secret",
			url:       "https://myvault.vault.azure.net/secrets/mysecret",
			wantScope: "https://vault.azure.net/.default",
		},
		// Storage - Blob
		{
			name:      "Blob storage",
			url:       "https://myaccount.blob.core.windows.net/container/blob",
			wantScope: "https://storage.azure.com/.default",
		},
		// Storage - Queue
		{
			name:      "Queue storage",
			url:       "https://myaccount.queue.core.windows.net/myqueue",
			wantScope: "https://storage.azure.com/.default",
		},
		// Storage - Table
		{
			name:      "Table storage",
			url:       "https://myaccount.table.core.windows.net/mytable",
			wantScope: "https://storage.azure.com/.default",
		},
		// Storage - File
		{
			name:      "File storage",
			url:       "https://myaccount.file.core.windows.net/myshare",
			wantScope: "https://storage.azure.com/.default",
		},
		// Data Lake
		{
			name:      "Data Lake",
			url:       "https://myaccount.dfs.core.windows.net/filesystem/path",
			wantScope: "https://storage.azure.com/.default",
		},
		// ACR
		{
			name:      "Container Registry",
			url:       "https://myregistry.azurecr.io/v2/repo/tags/list",
			wantScope: "https://management.azure.com/.default",
		},
		// Azure OpenAI
		{
			name:      "Azure OpenAI",
			url:       "https://myoai.openai.azure.com/openai/deployments/gpt4/chat/completions",
			wantScope: "https://cognitiveservices.azure.com/.default",
		},
		// Cognitive Services
		{
			name:      "Cognitive Services",
			url:       "https://mycs.cognitiveservices.azure.com/vision/v3.1/analyze",
			wantScope: "https://cognitiveservices.azure.com/.default",
		},
		// AI Services
		{
			name:      "AI Services",
			url:       "https://myai.services.ai.azure.com/api/projects/myproj",
			wantScope: "https://cognitiveservices.azure.com/.default",
		},
		// Azure DevOps
		{
			name:      "Azure DevOps",
			url:       "https://dev.azure.com/myorg/myproject/_apis/git/repos",
			wantScope: "499b84ac-1321-427f-aa17-267ca6975798/.default",
		},
		// PostgreSQL
		{
			name:      "PostgreSQL",
			url:       "https://myserver.postgres.database.azure.com:5432",
			wantScope: "https://ossrdbms-aad.database.windows.net/.default",
		},
		// MySQL
		{
			name:      "MySQL",
			url:       "https://myserver.mysql.database.azure.com:3306",
			wantScope: "https://ossrdbms-aad.database.windows.net/.default",
		},
		// Cosmos DB
		{
			name:      "Cosmos DB",
			url:       "https://myaccount.documents.azure.com:443/",
			wantScope: "https://cosmos.azure.com/.default",
		},
		// Event Hubs / Service Bus
		{
			name:      "Event Hubs",
			url:       "https://myns.servicebus.windows.net/myhub",
			wantScope: "https://eventhubs.azure.net/.default",
		},
		// App Configuration
		{
			name:      "App Configuration",
			url:       "https://myconfig.azconfig.io/kv/mykey",
			wantScope: "https://azconfig.io/.default",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			scopes, err := sd.ScopesForURL(tt.url)
			if err != nil {
				t.Fatalf("ScopesForURL(%q) error: %v", tt.url, err)
			}

			if len(scopes) != 1 {
				t.Fatalf("ScopesForURL(%q) returned %d scopes, want 1", tt.url, len(scopes))
			}

			if scopes[0] != tt.wantScope {
				t.Errorf("ScopesForURL(%q) = %q, want %q", tt.url, scopes[0], tt.wantScope)
			}
		})
	}
}

func TestScopeDetector_EmptyURL(t *testing.T) {
	t.Parallel()

	sd := NewScopeDetector(nil)
	_, err := sd.ScopesForURL("")
	if err == nil {
		t.Fatal("expected error for empty URL")
	}
}

func TestScopeDetector_NoMatch(t *testing.T) {
	t.Parallel()

	sd := NewScopeDetector(nil)
	_, err := sd.ScopesForURL("https://example.com/foo")
	if err == nil {
		t.Fatal("expected error for unknown host")
	}

	var scopeErr *ScopeDetectorError
	if !errors.As(err, &scopeErr) {
		t.Fatalf("error type = %T, want *ScopeDetectorError", err)
	}

	if scopeErr.URL != "https://example.com/foo" {
		t.Errorf("ScopeDetectorError.URL = %q, want %q", scopeErr.URL, "https://example.com/foo")
	}
}

func TestScopeDetector_MalformedURL(t *testing.T) {
	t.Parallel()

	sd := NewScopeDetector(nil)
	_, err := sd.ScopesForURL("://bad")
	if err == nil {
		t.Fatal("expected error for malformed URL")
	}
}

func TestScopeDetector_NoHost(t *testing.T) {
	t.Parallel()

	sd := NewScopeDetector(nil)
	_, err := sd.ScopesForURL("/relative/path")
	if err == nil {
		t.Fatal("expected error for URL without host")
	}
}

func TestScopeDetector_CustomRules(t *testing.T) {
	t.Parallel()

	sd := NewScopeDetector(&ScopeDetectorOptions{
		CustomRules: map[string]string{
			".custom.example.com": "https://custom.example.com/.default",
		},
	})

	scopes, err := sd.ScopesForURL("https://api.custom.example.com/v1/data")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(scopes) != 1 || scopes[0] != "https://custom.example.com/.default" {
		t.Errorf("scopes = %v, want [https://custom.example.com/.default]", scopes)
	}
}

func TestScopeDetector_CaseInsensitive(t *testing.T) {
	t.Parallel()

	sd := NewScopeDetector(nil)

	scopes, err := sd.ScopesForURL("https://MANAGEMENT.AZURE.COM/subscriptions")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(scopes) != 1 || scopes[0] != "https://management.azure.com/.default" {
		t.Errorf("scopes = %v, want [https://management.azure.com/.default]", scopes)
	}
}

func TestScopeDetector_URLWithPort(t *testing.T) {
	t.Parallel()

	sd := NewScopeDetector(nil)

	scopes, err := sd.ScopesForURL("https://myvault.vault.azure.net:443/secrets/key")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(scopes) != 1 || scopes[0] != "https://vault.azure.net/.default" {
		t.Errorf("scopes = %v, want [https://vault.azure.net/.default]", scopes)
	}
}

func TestScopeDetector_CustomRuleWithoutDotPrefix(t *testing.T) {
	t.Parallel()

	sd := NewScopeDetector(&ScopeDetectorOptions{
		CustomRules: map[string]string{
			"api.example.com": "https://example.com/.default",
		},
	})

	// Exact match should work.
	scopes, err := sd.ScopesForURL("https://api.example.com/v1/data")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(scopes) != 1 || scopes[0] != "https://example.com/.default" {
		t.Errorf("scopes = %v, want [https://example.com/.default]", scopes)
	}

	// Should NOT match a different host that merely ends with the same string.
	_, err = sd.ScopesForURL("https://evil-api.example.com/v1/data")
	if err == nil {
		t.Fatal("expected error: exact match should not match different host")
	}
}

func TestScopeDetector_EmptyCustomRuleIgnored(t *testing.T) {
	t.Parallel()

	sd := NewScopeDetector(&ScopeDetectorOptions{
		CustomRules: map[string]string{
			"": "https://catch-all.example.com/.default",
		},
	})

	// Empty key should be ignored, so unknown host still errors.
	_, err := sd.ScopesForURL("https://unknown.example.com/data")
	if err == nil {
		t.Fatal("expected error: empty custom rule should be ignored")
	}
}

func TestScopeDetector_DeterministicCustomRules(t *testing.T) {
	t.Parallel()

	// Create a detector with multiple custom rules and verify
	// that results are deterministic across multiple invocations.
	rules := map[string]string{
		".alpha.example.com": "https://alpha.example.com/.default",
		".beta.example.com":  "https://beta.example.com/.default",
		".gamma.example.com": "https://gamma.example.com/.default",
	}

	for i := 0; i < 10; i++ {
		sd := NewScopeDetector(&ScopeDetectorOptions{CustomRules: rules})

		scopes, err := sd.ScopesForURL("https://api.alpha.example.com/data")
		if err != nil {
			t.Fatalf("iteration %d: unexpected error: %v", i, err)
		}
		if scopes[0] != "https://alpha.example.com/.default" {
			t.Fatalf("iteration %d: scope = %q, want %q", i, scopes[0], "https://alpha.example.com/.default")
		}
	}
}
