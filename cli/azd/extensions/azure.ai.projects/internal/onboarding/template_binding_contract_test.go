// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package onboarding

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"azure.ai.projects/internal/synthesis"

	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTemplateBindingUsesExportedContentNotGeneratorOrARMHashEquality(t *testing.T) {
	raw, err := synthesis.ARMTemplate()
	require.NoError(t, err)
	var standard map[string]any
	require.NoError(t, json.Unmarshal(raw, &standard))
	metadata := cleanupObject(t, standard["metadata"])
	generator := cleanupObject(t, metadata["_generator"])
	generatorHash, ok := generator["templateHash"].(string)
	require.True(t, ok)
	digest, err := TemplateDigest(standard)
	require.NoError(t, err)
	for _, tc := range []struct {
		name, digest, version string
		mutate                func(map[string]any)
		status                int
		wantError             string
	}{
		{"legacy exact trusted standard", "", generatorHash, nil, 200, ""},
		{"new content digest", digest, generatorHash, nil, 200, ""},
		{"metadata is not ownership proof", "", generatorHash, func(template map[string]any) {
			template["resources"] = map[string]any{}
		}, 200, "actual template binding"},
		{"new digest rejects changed content", digest, generatorHash, func(template map[string]any) {
			template["outputs"] = map[string]any{}
		}, 200, "actual template binding"},
		{"legacy unknown version", "", "unknown-version", nil, 200, "no trusted standard version"},
		{"export denied", digest, generatorHash, nil, 403, "read exact recorded deployment template"},
		{"export missing", digest, generatorHash, nil, 404, "read exact recorded deployment template"},
		{"export lacks template", digest, generatorHash, nil, 200, "template content is unavailable"},
		{"generator metadata changes do not change content", digest, generatorHash, func(template map[string]any) {
			delete(template, "metadata")
		}, 200, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var template map[string]any
			require.NoError(t, json.Unmarshal(raw, &template))
			if tc.mutate != nil {
				tc.mutate(template)
			}
			if tc.name == "export lacks template" {
				template = nil
			}
			reads := 0
			options := contractARMOptions(func(request *http.Request) (*http.Response, error) {
				reads++
				require.Equal(t, http.MethodPost, request.Method, "export is a read-only POST, not a deployment write")
				require.Equal(t, "/subscriptions/target/providers/Microsoft.Resources/deployments/exact/exportTemplate",
					request.URL.Path)
				require.Equal(t, "2021-04-01", request.URL.Query().Get("api-version"))
				if tc.status != 200 {
					body := `{"error":{"code":"AuthorizationFailed"}}`
					return contractARMResponse(request, tc.status, body), nil
				}
				body, err := json.Marshal(map[string]any{"template": template})
				require.NoError(t, err)
				return contractARMResponse(request, http.StatusOK, string(body)), nil
			})
			client, err := armresources.NewDeploymentsClient("target", contractCredential{}, options)
			require.NoError(t, err)
			err = verifyAttemptTemplate(t.Context(), client, "exact",
				FoundryAttempt{TemplateHash: tc.version, TemplateDigest: tc.digest})
			if tc.wantError == "" {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, tc.wantError)
			}
			assert.Equal(t, 1, reads)
		})
	}
}

func TestTemplateDigestRetainsResourceMetadata(t *testing.T) {
	template := map[string]any{
		"$schema":  "https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#",
		"metadata": map[string]any{"_generator": map[string]any{"templateHash": "compiler"}},
		"resources": map[string]any{
			"connection": map[string]any{
				"properties": map[string]any{"metadata": map[string]any{"ResourceId": "actual-registry"}},
			},
		},
	}
	before, err := TemplateDigest(template)
	require.NoError(t, err)
	delete(template, "metadata")
	after, err := TemplateDigest(template)
	require.NoError(t, err)
	assert.Equal(t, before, after, "only template-level non-executable metadata is excluded")
	connection := cleanupObject(t, cleanupObject(t, template["resources"])["connection"])
	cleanupObject(t, cleanupObject(t, connection["properties"])["metadata"])["ResourceId"] = "changed-registry"
	after, err = TemplateDigest(template)
	require.NoError(t, err)
	assert.NotEqual(t, before, after, "resource metadata is part of the actual request and must stay bound")
}

func TestTemplateDigestCanonicalizesOnlyARMDeclarationTypes(t *testing.T) {
	const source = `{
		"$schema":"https://schema.management.azure.com/schemas/2018-05-01/subscriptionDeploymentTemplate.json#",
		"contentVersion":"1.0.0.0","languageVersion":"2.0",
		"metadata":{"_generator":{"templateHash":"9622934973028600007"}},
		"definitions":{"hosting":{"type":"object","properties":{"enabled":{"type":"bool"}}}},
		"parameters":{"mode":{"type":"string","defaultValue":"String"}},
		"outputs":{"id":{"type":"string","value":"[parameters('mode')]"}},
		"variables":{"authored":{"type":"string","metadata":{"owner":"unchanged"}}},
		"resources":{"nested":{"type":"Microsoft.Resources/deployments","properties":{"template":{
			"$schema":"https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#",
			"metadata":{"_generator":{"templateHash":"3250899696596760951"}},
			"parameters":{"name":{"type":"string"}},"resources":{}
		}}}}
	}`
	// Actual export shape: symbolic resources, no root metadata, capitalized root declaration types,
	// while nested templates retain their own generator metadata.
	const exported = `{
		"$schema":"https://schema.management.azure.com/schemas/2018-05-01/subscriptionDeploymentTemplate.json#",
		"contentVersion":"1.0.0.0","languageVersion":"2.0",
		"definitions":{"hosting":{"type":"Object","properties":{"enabled":{"type":"Bool"}}}},
		"parameters":{"mode":{"type":"String","defaultValue":"String"}},
		"outputs":{"id":{"type":"String","value":"[parameters('mode')]"}},
		"variables":{"authored":{"type":"string","metadata":{"owner":"unchanged"}}},
		"resources":{"nested":{"type":"Microsoft.Resources/deployments","properties":{"template":{
			"$schema":"https://schema.management.azure.com/schemas/2019-04-01/deploymentTemplate.json#",
			"metadata":{"_generator":{"templateHash":"3250899696596760951"}},
			"parameters":{"name":{"type":"string"}},"resources":{}
		}}}}
	}`
	var template, actual map[string]any
	require.NoError(t, json.Unmarshal([]byte(source), &template))
	require.NoError(t, json.Unmarshal([]byte(exported), &actual))
	expected, err := TemplateDigest(template)
	require.NoError(t, err)
	digest, err := TemplateDigest(actual)
	require.NoError(t, err)
	assert.Equal(t, expected, digest)
	for _, replacement := range []string{
		strings.Replace(exported, `"defaultValue":"String"`, `"defaultValue":"string"`, 1),
		strings.Replace(exported, `"type":"Microsoft.Resources/deployments"`, `"type":"changed"`, 1),
		strings.Replace(exported, `"authored":{"type":"string"`, `"authored":{"type":"String"`, 1),
		strings.Replace(exported, `"owner":"unchanged"`, `"owner":"changed"`, 1),
		strings.Replace(exported, `"resources":{}`, `"resources":[]`, 1),
	} {
		var changed map[string]any
		require.NoError(t, json.Unmarshal([]byte(replacement), &changed))
		digest, err := TemplateDigest(changed)
		require.NoError(t, err)
		assert.NotEqual(t, expected, digest, "normalization must not erase authored content changes")
	}
}

func TestLegacyAttemptRecoversDifferentARMHashWithoutRewritingJournal(t *testing.T) {
	f := newCleanupARM(t, false, false)
	f.deploy(t)
	state, err := f.base.journal.Load(t.Context())
	require.NoError(t, err)
	raw, err := synthesis.ARMTemplate()
	require.NoError(t, err)
	var template map[string]any
	require.NoError(t, json.Unmarshal(raw, &template))
	generator := cleanupObject(t, cleanupObject(t, template["metadata"])["_generator"])
	hash, ok := generator["templateHash"].(string)
	require.True(t, ok)
	state.Foundry.Spec.TemplateHash, state.Foundry.Spec.TemplateDigest = hash, ""
	for index := range state.Foundry.Attempts {
		state.Foundry.Attempts[index].TemplateHash = hash
		state.Foundry.Attempts[index].TemplateDigest = ""
		state.Foundry.Attempts[index].ARMTemplateHash = ""
		root := f.roots[strings.ToLower(state.Foundry.Attempts[index].ID)]
		properties := cleanupObject(t, root["properties"])
		properties["templateHash"] = "6828760392490666086"
		properties["template"] = template
	}
	require.NoError(t, f.base.journal.Save(t.Context(), state))
	before, err := json.Marshal(state)
	require.NoError(t, err)
	_, err = ResolveRecoveryContext(t.Context(), f.base.plan, contractCredential{}, f.options, f.base.journal)
	require.NoError(t, err)
	after, err := f.base.journal.Load(t.Context())
	require.NoError(t, err)
	encoded, err := json.Marshal(after)
	require.NoError(t, err)
	assert.JSONEq(t, string(before), string(encoded), "read-only recovery must not rewrite the old hash or progress")
	require.NoError(t, f.lifecycle.Observe(t.Context(), f.base.plan))
	confirmed, err := f.base.journal.Load(t.Context())
	require.NoError(t, err)
	assert.Equal(t, hash, confirmed.Foundry.Attempts[0].TemplateHash, "preserve the old generator-version binding")
	assert.Equal(t, "6828760392490666086", confirmed.Foundry.Attempts[0].ARMTemplateHash,
		"persist ARM's independent hash only after actual exported content and lifecycle evidence are verified")
}
