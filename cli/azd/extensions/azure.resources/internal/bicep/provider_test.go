// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package bicep

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resources/armresources"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/azure/azure-dev/cli/azd/pkg/grpcbroker"
	"github.com/stretchr/testify/require"
)

const groupID = "/subscriptions/sub/resourceGroups/generated"
const linkedID = "/subscriptions/sub/resourceGroups/existing/providers/Microsoft.Storage/storageAccounts/linked"

type fakeBackend struct {
	deployment    armresources.DeploymentExtended
	getErr        error
	groupValue    armresources.ResourceGroup
	groupErr      error
	contents      []string
	changes       []*armresources.WhatIfChange
	previewErr    error
	previewInput  armresources.DeploymentWhatIf
	deployErr     error
	deployInput   armresources.Deployment
	deleteErr     error
	deployed      bool
	deletedGroups []string
	deletedRecord bool
	hintName      string
	hinted        armresources.DeploymentExtended
}

func (f *fakeBackend) get(_ context.Context, name string) (armresources.DeploymentExtended, error) {
	if f.hintName != "" && name == f.hintName {
		return f.hinted, nil
	}
	return f.deployment, f.getErr
}

func (f *fakeBackend) deploy(
	_ context.Context, _ string, request armresources.Deployment, progress grpcbroker.ProgressFunc,
) (armresources.DeploymentExtended, error) {
	f.deployed = true
	f.deployInput = request
	if request.Properties.Mode == nil || *request.Properties.Mode != armresources.DeploymentModeIncremental {
		return armresources.DeploymentExtended{}, errors.New("deployment must be incremental")
	}
	report(progress, "deployed")
	return f.deployment, f.deployErr
}

func (f *fakeBackend) preview(
	_ context.Context, _ string, request armresources.DeploymentWhatIf, progress grpcbroker.ProgressFunc,
) ([]*armresources.WhatIfChange, error) {
	f.previewInput = request
	report(progress, "preview")
	return f.changes, f.previewErr
}

func (f *fakeBackend) group(context.Context, string) (armresources.ResourceGroup, error) {
	return f.groupValue, f.groupErr
}

func (f *fakeBackend) deleteGroup(_ context.Context, name string, progress grpcbroker.ProgressFunc) error {
	f.deletedGroups = append(f.deletedGroups, name)
	report(progress, "deleted")
	if f.deleteErr == nil {
		f.contents = nil
	}
	return f.deleteErr
}

func (f *fakeBackend) deleteDeployment(context.Context, string, grpcbroker.ProgressFunc) error {
	f.deletedRecord = true
	return nil
}

func readyProvider(t *testing.T, azure *fakeBackend) *Provider {
	t.Helper()
	name := deploymentName("example", "dev", "data")
	p := &Provider{
		options: &azdext.ProvisioningOptions{Name: "data"}, name: name,
		envName: "dev", projectName: "example", path: "testdata", module: "main",
		scope: &scope{
			subscriptionID: "sub", tenantID: "user-access-tenant", location: "eastus",
			principalID: "object-id", principalType: "User",
		},
		azure: azure,
		template: map[string]any{
			"$schema": "https://schema.management.azure.com/schemas/2018-05-01/subscriptionDeploymentTemplate.json#",
			"parameters": map[string]any{
				"environmentName": map[string]any{"type": "string"},
				"principalId":     map[string]any{"type": "string"},
				"serviceExists":   map[string]any{"type": "bool"},
				"password":        map[string]any{"type": "secureString"},
				"settings":        map[string]any{"type": "object"},
			},
			"resources": []any{map[string]any{
				"type": "Microsoft.Resources/resourceGroups", "name": "generated", "tags": "[variables('tags')]",
			}},
			"outputs": map[string]any{"AZURE_RESOURCE_STORAGE_ID": map[string]any{"type": "string"}},
		},
	}
	p.client = newHostClient(t, &environmentServer{values: map[string]string{
		"AZURE_SUBSCRIPTION_ID": "sub", "AZURE_LOCATION": "eastus",
		"QUOTED": "contains \"quotes\"\nand newlines", "PASSWORD": "saved-password",
	}}, &promptServer{})
	return p
}

func deployedFixture() armresources.DeploymentExtended {
	return armresources.DeploymentExtended{Properties: &armresources.DeploymentPropertiesExtended{
		Outputs: map[string]any{
			"AZURE_RESOURCE_STORAGE_ID": map[string]any{"type": "String", "value": linkedID},
			"OBJECT":                    map[string]any{"type": "Object", "value": map[string]any{"enabled": true}},
			"ARRAY":                     map[string]any{"type": "Array", "value": []any{1.0, "two"}},
			"BOOL":                      map[string]any{"type": "Bool", "value": true},
		},
		OutputResources: []*armresources.ResourceReference{{ID: new(groupID)}},
	}}
}

func TestDeploymentNameIsolation(t *testing.T) {
	names := []string{
		deploymentName("example", "dev", "data"), deploymentName("example", "dev", "app"),
		deploymentName("example", "other", "data"), deploymentName("example", "dev", ""),
		deploymentName("example", "a\x00b", "c"), deploymentName("example", "a", "b\x00c"),
		deploymentName("other-project", "dev", "data"),
	}
	for _, name := range names {
		if len(name) > 64 {
			t.Fatalf("deployment name too long: %q", name)
		}
	}
	slices.Sort(names)
	if len(slices.Compact(names)) != len(names) {
		t.Fatal("deployment identities collided")
	}
}

func TestParametersGeneratedSubstitutions(t *testing.T) {
	p := readyProvider(t, &fakeBackend{})
	parameters, err := p.Parameters(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	byName := map[string]*azdext.ProvisioningParameter{}
	for _, parameter := range parameters {
		byName[parameter.Name] = parameter
	}
	if !byName["password"].Secret || byName["password"].Value != "saved-password" {
		t.Fatalf("secure parameter metadata lost: %+v", byName["password"])
	}
	if byName["serviceExists"].Value != "false" {
		t.Fatalf("bool fallback was not coerced: %+v", byName["serviceExists"])
	}
	if len(byName["principalId"].EnvVarMapping) != 0 {
		t.Fatal("global principal ID must not be a CI environment mapping")
	}
	settings, ok := p.params["settings"].(map[string]any)
	if !ok {
		t.Fatal("settings parameter wrapper missing")
	}
	value, ok := settings["value"].(map[string]any)
	if !ok || value["quoted"] != "contains \"quotes\"\nand newlines" {
		t.Fatal("nested quoted environment value was corrupted")
	}
	require.NoError(t, p.setEnv(t.Context(), "PASSWORD", "changed-value"))
	repeated, err := p.Parameters(t.Context())
	require.NoError(t, err)
	require.Equal(t, parameters, repeated, "parameters must be cached within a provider session")
}

func TestParametersTemplateDefaults(t *testing.T) {
	for _, test := range []struct {
		name  string
		input string
	}{
		{name: "missing value", input: `{"parameters": {}}`},
		{name: "empty environment value", input: `{"parameters": {"optional": {"value": "${MISSING}"}}}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := readyProvider(t, &fakeBackend{})
			p.path = t.TempDir()
			p.template["parameters"] = map[string]any{
				"optional": map[string]any{"type": "string", "defaultValue": "fallback"},
			}
			require.NoError(t, os.WriteFile(filepath.Join(p.path, "main.parameters.json"), []byte(test.input), 0o600))

			parameters, err := p.Parameters(t.Context())
			require.NoError(t, err)
			require.Empty(t, parameters)
			require.Empty(t, p.params)
			require.NotNil(t, p.params)
		})
	}
}

func TestParametersReferenceMetadata(t *testing.T) {
	p := readyProvider(t, &fakeBackend{})
	p.path = t.TempDir()
	vaultID := "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.KeyVault/vaults/vault"
	require.NoError(t, p.setEnv(t.Context(), "VAULT_ID", vaultID))
	p.template["parameters"] = map[string]any{"password": map[string]any{"type": "secureString"}}
	input := `{"parameters": {"password": {"reference": {
		"keyVault": {"id": "${VAULT_ID}"}, "secretName": "${SECRET_NAME:=password}", "secretVersion": "${SECRET_NAME}"
	}}}}`
	require.NoError(t, os.WriteFile(filepath.Join(p.path, "main.parameters.json"), []byte(input), 0o600))

	parameters, err := p.Parameters(t.Context())
	require.NoError(t, err)
	require.Equal(t, []*azdext.ProvisioningParameter{{
		Name: "password", Value: "null", Secret: true,
		EnvVarMapping: []string{"SECRET_NAME", "VAULT_ID"}, UsingEnvVarMapping: true,
	}}, parameters)
	require.Equal(t, map[string]any{"reference": map[string]any{
		"keyVault": map[string]any{"id": vaultID}, "secretName": "password", "secretVersion": "",
	}}, p.params["password"])
}

func TestParametersRejectValueAndReference(t *testing.T) {
	p := readyProvider(t, &fakeBackend{})
	p.path = t.TempDir()
	p.template["parameters"] = map[string]any{"password": map[string]any{"type": "secureString"}}
	input := `{"parameters": {"password": {"value": "password", "reference": {}}}}`
	require.NoError(t, os.WriteFile(filepath.Join(p.path, "main.parameters.json"), []byte(input), 0o600))

	_, err := p.Parameters(t.Context())
	require.EqualError(t, err, `parameter "password" cannot contain both value and reference`)
	require.Nil(t, p.params)
	require.Nil(t, p.metadata)
}

func TestResolveEnvironmentDefaults(t *testing.T) {
	for _, test := range []struct {
		name, input, want string
		values            map[string]string
	}{
		{name: "missing", input: "${TEXT:=fallback}", want: "fallback"},
		{name: "empty", input: "${TEXT:=fallback}", values: map[string]string{"TEXT": ""}, want: "fallback"},
		{name: "set", input: "${TEXT:=fallback}", values: map[string]string{"TEXT": "value"}, want: "value"},
		{name: "embedded", input: "Hello, ${TEXT:=world}!", want: "Hello, world!"},
		{name: "quoted default", input: "${TEXT:='local'}", want: "'local'"},
		{name: "empty default", input: "${TEXT:=}", want: ""},
		{name: "legacy missing", input: "${TEXT=fallback}", want: "fallback"},
		{name: "legacy empty", input: "${TEXT=fallback}", values: map[string]string{"TEXT": ""}, want: ""},
		{name: "plain", input: "${TEXT}", values: map[string]string{"TEXT": "value"}, want: "value"},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := &Provider{}
			var mapping []string
			got, err := p.resolve(t.Context(), test.input, test.values, &mapping)
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("resolved %q to %q, want %q", test.input, got, test.want)
			}
			if !reflect.DeepEqual(mapping, []string{"TEXT"}) {
				t.Fatalf("environment mapping lost: %v", mapping)
			}
		})
	}
}

func TestResolveRejectsCommands(t *testing.T) {
	tests := []struct {
		name, input string
	}{
		{name: "former password command", input: "$(secretOrRandomPassword vault password)"},
		{name: "unknown command", input: "$(shell secret)"},
		{name: "malformed command", input: "$(shell secret"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := readyProvider(t, &fakeBackend{})
			mapping := []string{}
			value, err := p.resolve(t.Context(), tt.input, nil, &mapping)
			require.ErrorContains(t, err, "command substitutions are not supported")
			require.Nil(t, value)
		})
	}
}

func TestProvisioningValueUsesStableStringFields(t *testing.T) {
	for _, test := range []struct {
		name     string
		value    any
		expected string
	}{
		{name: "empty string", value: "", expected: ""},
		{name: "quoted string", value: "contains \"quotes\"\nand newlines", expected: "contains \"quotes\"\nand newlines"},
		{name: "JSON-looking string", value: `{"enabled":true}`, expected: `{"enabled":true}`},
		{name: "boolean-looking string", value: "true", expected: "true"},
		{name: "boolean", value: true, expected: "true"},
		{name: "number", value: 42, expected: "42"},
		{name: "object", value: map[string]any{"enabled": true}, expected: `{"enabled":true}`},
		{name: "array", value: []any{1, "two"}, expected: `[1,"two"]`},
	} {
		t.Run(test.name, func(t *testing.T) {
			actual, err := provisioningValue(test.value)
			require.NoError(t, err)
			require.Equal(t, test.expected, actual)
		})
	}
}

func TestProvisioningValueRejectsUnsupportedValues(t *testing.T) {
	_, err := provisioningValue(make(chan int))
	require.Error(t, err)
}

func TestStateStringOutputsAndResourceIDs(t *testing.T) {
	p := readyProvider(t, &fakeBackend{deployment: deployedFixture()})
	result, err := p.State(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	for key, expected := range map[string]string{
		"BOOL": "true", "OBJECT": `{"enabled":true}`, "ARRAY": `[1,"two"]`,
		"AZURE_RESOURCE_STORAGE_ID": linkedID,
	} {
		if actual := result.State.Outputs[key]; actual == nil || actual.Value != expected {
			t.Errorf("output %s = %v; want %s", key, actual, expected)
		}
	}
	if len(result.State.Resources) != 2 {
		t.Fatalf("resource IDs omitted: %v", result.State.Resources)
	}
	if _, err := p.State(t.Context(), &azdext.ProvisioningStateOptions{Hint: "unrelated"}); err == nil {
		t.Fatal("unrelated state hint accepted")
	}
}

func TestHintedStateKeepsDeploymentIdentity(t *testing.T) {
	hinted := deployedFixture()
	hinted.Tags = map[string]*string{
		"azd-project-name": new("example"), "azd-env-name": new("dev"), "azd-layer-name": new("data"),
	}
	azure := &fakeBackend{
		hintName: "dev-data-1793000000", hinted: hinted, getErr: &azcore.ResponseError{StatusCode: 404},
	}
	p := readyProvider(t, azure)
	name := p.name

	state, err := p.State(t.Context(), &azdext.ProvisioningStateOptions{Hint: azure.hintName})
	require.NoError(t, err)
	require.NotEmpty(t, state.State.Outputs)
	require.Equal(t, name, p.name)

	// Destroy still targets our own deployment, which doesn't exist, so nothing is deleted.
	_, err = p.Destroy(t.Context(), &azdext.ProvisioningDestroyOptions{Force: true}, nil)
	require.NoError(t, err)
	require.False(t, azure.deletedRecord)
	require.Empty(t, azure.deletedGroups)
}

func TestHintedStateRejectsAnotherProject(t *testing.T) {
	hinted := deployedFixture()
	hinted.Tags = map[string]*string{
		"azd-project-name": new("other"), "azd-env-name": new("dev"), "azd-layer-name": new("data"),
	}
	p := readyProvider(t, &fakeBackend{hintName: "dev-data-1793000000", hinted: hinted})
	_, err := p.State(t.Context(), &azdext.ProvisioningStateOptions{Hint: "dev-data-1793000000"})
	require.Error(t, err)
}

func TestDeployRefusesUntaggedGroupWithMatchingEnvironment(t *testing.T) {
	azure := &fakeBackend{
		deployment: deployedFixture(),
		changes:    []*armresources.WhatIfChange{{ResourceID: new(groupID), ChangeType: new(armresources.ChangeTypeModify)}},
		groupValue: armresources.ResourceGroup{Tags: map[string]*string{"azd-env-name": new("dev")}},
	}
	p := readyProvider(t, azure)
	_, err := p.Deploy(t.Context(), nil)
	require.ErrorContains(t, err, "refusing to adopt existing resource group")
	require.False(t, azure.deployed)
}

func TestPlannedOutputsCompileOnce(t *testing.T) {
	p := New(nil, nil)
	if err := p.Initialize(t.Context(), ".", &azdext.ProvisioningOptions{Path: "testdata"}); err != nil {
		t.Fatal(err)
	}
	calls := 0
	p.compile = func(_ context.Context, path string) ([]byte, error) {
		calls++
		if !strings.HasSuffix(path, "main.bicep") {
			t.Fatalf("unexpected compilation path %q", path)
		}
		return json.Marshal(map[string]any{
			"$schema": "https://schema.management.azure.com/schemas/2018-05-01/subscriptionDeploymentTemplate.json#",
			"outputs": map[string]any{"B": nil, "A": nil},
		})
	}
	for range 2 {
		outputs, err := p.PlannedOutputs(t.Context())
		if err != nil || len(outputs) != 2 || outputs[0].Name != "A" || outputs[1].Name != "B" {
			t.Fatalf("planned outputs = %v, error %v", outputs, err)
		}
	}
	if calls != 1 {
		t.Fatalf("compiled %d times", calls)
	}
}

func TestDeployOwnershipAndProgress(t *testing.T) {
	for _, test := range []struct {
		name, owner string
		missing     bool
		wantErr     bool
	}{
		{name: "new group", missing: true},
		{name: "owned group"},
		{name: "unowned existing group", owner: "foreign", wantErr: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			azure := &fakeBackend{
				deployment: deployedFixture(),
				changes: []*armresources.WhatIfChange{
					{ResourceID: new(groupID), ChangeType: new(armresources.ChangeTypeCreate)},
				},
			}
			p := readyProvider(t, azure)
			if test.missing {
				azure.groupErr = &azcore.ResponseError{StatusCode: 404}
			} else {
				owner := p.name
				if test.owner != "" {
					owner = test.owner
				}
				azure.groupValue.Tags = map[string]*string{ownerTag: new(owner)}
			}
			progress := []string{}
			_, err := p.Deploy(t.Context(), func(message string) { progress = append(progress, message) })
			if (err != nil) != test.wantErr || azure.deployed == test.wantErr {
				t.Fatalf("deployed %v; error %v; want error %v", azure.deployed, err, test.wantErr)
			}
			if len(progress) == 0 {
				t.Fatal("progress not forwarded")
			}
		})
	}
}

func TestDestroyDoesNotDeleteGroupsOfLinkedResources(t *testing.T) {
	azure := &fakeBackend{deployment: deployedFixture()}
	p := readyProvider(t, azure)
	azure.groupValue.Tags = map[string]*string{ownerTag: new(p.name)}
	result, err := p.Destroy(t.Context(), &azdext.ProvisioningDestroyOptions{Force: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(azure.deletedGroups, []string{"generated"}) || !azure.deletedRecord {
		t.Fatalf("unsafe delete scope: groups %v record %v", azure.deletedGroups, azure.deletedRecord)
	}
	if !slices.Contains(result.InvalidatedEnvKeys, "AZURE_RESOURCE_STORAGE_ID") {
		t.Fatal("deployment outputs were not invalidated")
	}
}

func TestDestroyRejectsUnsafeScopeAndUnsupportedPurge(t *testing.T) {
	for _, test := range []struct {
		name    string
		groupID string
		purge   bool
	}{
		{name: "another subscription", groupID: "/subscriptions/other/resourceGroups/generated"},
		{name: "unsupported purge", purge: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			azure := &fakeBackend{deployment: deployedFixture()}
			p := readyProvider(t, azure)
			if test.groupID != "" {
				azure.deployment.Properties.OutputResources[0].ID = new(test.groupID)
			}
			_, err := p.Destroy(t.Context(), &azdext.ProvisioningDestroyOptions{Force: true, Purge: test.purge}, nil)
			if err == nil || len(azure.deletedGroups) != 0 || azure.deletedRecord {
				t.Fatalf("unsafe deletion accepted: %v", err)
			}
		})
	}
}

func TestDestroyFailureRetainsDeploymentForRetry(t *testing.T) {
	azure := &fakeBackend{deployment: deployedFixture(), deleteErr: errors.New("delete failed")}
	p := readyProvider(t, azure)
	azure.groupValue.Tags = map[string]*string{ownerTag: new(p.name)}
	_, err := p.Destroy(t.Context(), &azdext.ProvisioningDestroyOptions{Force: true}, nil)
	if err == nil || azure.deletedRecord {
		t.Fatal("failed deletion discarded its retryable deployment record")
	}
}

func TestDestroyDeletesAllResourcesInRecordedGroups(t *testing.T) {
	removedID := groupID + "/providers/Microsoft.Storage/storageAccounts/removed"
	linkedInGroup := groupID + "/providers/Microsoft.Storage/storageAccounts/linked"
	for _, test := range []struct {
		name string
		id   string
	}{
		{"removed from configuration", removedID},
		{"Azure-created monitoring resource",
			groupID + "/providers/Microsoft.AlertsManagement/smartDetectorAlertRules/Failure Anomalies"},
		{"linked resource inside owned group", linkedInGroup},
	} {
		t.Run(test.name, func(t *testing.T) {
			azure := &fakeBackend{deployment: deployedFixture(), contents: []string{test.id}}
			if test.id == linkedInGroup {
				azure.deployment.Properties.Parameters = map[string]any{
					"existingStorageId": map[string]any{"value": linkedInGroup},
				}
				azure.deployment.Properties.OutputResources = append(
					azure.deployment.Properties.OutputResources, &armresources.ResourceReference{ID: new(linkedInGroup)})
			}
			p := readyProvider(t, azure)
			_, err := p.Destroy(t.Context(), &azdext.ProvisioningDestroyOptions{Force: true}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(azure.deletedGroups, []string{"generated"}) ||
				!azure.deletedRecord || len(azure.contents) != 0 {
				t.Fatal("destroy did not delete the entire recorded group and its deployment record")
			}
		})
	}
}

func TestDestroyDoesNotRequireGroupOwnershipTags(t *testing.T) {
	for _, owner := range []string{"", "another-layer"} {
		t.Run(owner, func(t *testing.T) {
			azure := &fakeBackend{deployment: deployedFixture()}
			azure.groupValue.Tags = map[string]*string{ownerTag: new(owner)}
			p := readyProvider(t, azure)
			if _, err := p.Destroy(t.Context(), &azdext.ProvisioningDestroyOptions{Force: true}, nil); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(azure.deletedGroups, []string{"generated"}) || !azure.deletedRecord {
				t.Fatal("recorded group was not deleted")
			}
		})
	}
}

func TestEntryScopedOutputsRetainResourceIdentity(t *testing.T) {
	azure := &fakeBackend{deployment: deployedFixture()}
	azure.deployment.Properties.Outputs = map[string]any{
		"AZURE_RESOURCES_DATA__RESOURCE_STORAGE_ID": map[string]any{"type": "String", "value": linkedID},
	}
	p := readyProvider(t, azure)
	state, err := p.State(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	found := slices.ContainsFunc(state.State.Resources, func(resource *azdext.ProvisioningResource) bool {
		return resource.Id == linkedID
	})
	if !found {
		t.Fatal("entry-scoped output lost its ARM resource identity")
	}
	azure.groupValue.Tags = map[string]*string{ownerTag: new(p.name)}
	result, err := p.Destroy(t.Context(), &azdext.ProvisioningDestroyOptions{Force: true}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(result.InvalidatedEnvKeys, []string{"AZURE_RESOURCES_DATA__RESOURCE_STORAGE_ID"}) {
		t.Fatalf("down invalidated another entry's keys: %v", result.InvalidatedEnvKeys)
	}
}

func TestDestroyPurgeGuidanceBeforeHostAccess(t *testing.T) {
	_, err := New(nil, nil).Destroy(t.Context(), &azdext.ProvisioningDestroyOptions{Purge: true}, nil)
	local, ok := errors.AsType[*azdext.LocalError](err)
	if !ok {
		t.Fatalf("expected a local validation error, got %v", err)
	}
	if local.Code != "unsupported_purge" || local.Category != "user" {
		t.Fatalf("unexpected purge error classification: %+v", local)
	}
	want := "Omit --purge to retain soft-deleted resources. " +
		"Use separately authored native Bicep infrastructure if you need purge support; " +
		"azd infra gen cannot export resources provider configuration."
	if local.Suggestion != want {
		t.Fatalf("purge guidance = %q, want %q", local.Suggestion, want)
	}
}

func TestRequestDoesNotMutateCompiledTemplate(t *testing.T) {
	p := readyProvider(t, &fakeBackend{})
	for range 2 {
		if _, err := p.request(t.Context()); err != nil {
			t.Fatal(err)
		}

	}
	resources, ok := p.template["resources"].([]any)
	if !ok || len(resources) != 1 {
		t.Fatal("invalid compiled resources")
	}
	group, ok := resources[0].(map[string]any)
	if !ok || group["tags"] != "[variables('tags')]" {
		t.Fatal("request ownership injection mutated compiler cache")
	}
}

func TestRequestRetainsCoreDeploymentDiscoveryTags(t *testing.T) {
	p := readyProvider(t, &fakeBackend{})
	p.projectName = "example"
	request, err := p.request(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	for key, expected := range map[string]string{
		"azd-project-name": "example", "azd-env-name": "dev", "azd-layer-name": "data",
	} {
		if pointerValue(request.Tags[key]) != expected {
			t.Errorf("deployment tag %s = %q; want %q", key, pointerValue(request.Tags[key]), expected)
		}
	}
}

func TestMissingBicepAndClose(t *testing.T) {
	t.Setenv("AZD_BICEP_TOOL_PATH", "./does-not-exist-bicep")
	_, err := compileBicep(t.Context(), "testdata/main.bicep")
	toolError, ok := errors.AsType[*azdext.ToolError](err)
	if !ok || toolError.Kind != azdext.ToolErrorKindMissing {
		t.Fatalf("missing Bicep error = %v", err)
	}
	p := readyProvider(t, &fakeBackend{})
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := p.Parameters(t.Context()); err == nil {
		t.Fatal("closed provider remained usable")
	}
}

func TestPreviewStructuredChangesAndFailures(t *testing.T) {
	azure := &fakeBackend{changes: []*armresources.WhatIfChange{
		{ResourceID: new(groupID), ChangeType: new(armresources.ChangeTypeCreate)},
		{ResourceID: new(linkedID), ChangeType: new(armresources.ChangeTypeNoChange)},
	}}
	p := readyProvider(t, azure)
	progress := []string{}
	result, err := p.Preview(t.Context(), func(message string) { progress = append(progress, message) })
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Preview.Changes) != 2 || result.Preview.Changes[0].ChangeType != "Create" ||
		result.Preview.Changes[1].ResourceType != "Microsoft.Storage/storageAccounts" || len(progress) == 0 {
		t.Fatalf("preview lost structured change information: %v", result)
	}
	azure.previewErr = context.Canceled
	if _, err := p.Preview(t.Context(), nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("preview cancellation was lost: %v", err)
	}
	azure.previewErr = nil
	azure.changes = []*armresources.WhatIfChange{{ResourceID: new(groupID)}}
	if _, err := p.Preview(t.Context(), nil); err == nil {
		t.Fatal("incomplete what-if change was silently accepted")
	}
}

func TestStateMissingAndServiceFailures(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  error
		want bool
	}{
		{name: "missing", err: &azcore.ResponseError{StatusCode: 404}},
		{name: "access denied", err: &azcore.ResponseError{StatusCode: 403}, want: true},
		{name: "canceled", err: context.Canceled, want: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			p := readyProvider(t, &fakeBackend{getErr: tt.err})
			result, err := p.State(t.Context(), nil)
			if (err != nil) != tt.want {
				t.Fatalf("state error = %v; want error %v", err, tt.want)
			}
			if !tt.want && (result.State == nil || len(result.State.Resources) != 0) {
				t.Fatalf("missing deployment should have empty state: %v", result)
			}
		})
	}
}

func TestInitializeRejectsUnsupportedOptions(t *testing.T) {
	for _, tt := range []struct {
		name    string
		options *azdext.ProvisioningOptions
	}{
		{name: "nil"},
		{name: "no path", options: &azdext.ProvisioningOptions{}},
		{name: "module traversal", options: &azdext.ProvisioningOptions{Path: ".", Module: "../main"}},
		{name: "stacks", options: &azdext.ProvisioningOptions{
			Path: ".", DeploymentStacks: map[string]string{"actionOnUnmanage": "deleteAll"},
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := New(nil, nil).Initialize(t.Context(), ".", tt.options); err == nil {
				t.Fatal("unsupported options silently accepted")
			}
		})
	}
}

func TestCoerceGeneratedParameterTypes(t *testing.T) {
	for _, tt := range []struct {
		kind  string
		value any
		want  any
	}{
		{kind: "bool", value: "false", want: false},
		{kind: "int", value: "42", want: 42.0},
		{kind: "array", value: `["a",true]`, want: []any{"a", true}},
		{kind: "object", value: `{"enabled":true}`, want: map[string]any{"enabled": true}},
		{kind: "secureString", value: "secret", want: "secret"},
	} {
		t.Run(tt.kind, func(t *testing.T) {
			actual, err := coerce(tt.value, tt.kind)
			if err != nil || !reflect.DeepEqual(actual, tt.want) {
				t.Fatalf("coerce = %v, %v; want %v", actual, err, tt.want)
			}
		})
	}
	if _, err := coerce("not-a-bool", "bool"); err == nil {
		t.Fatal("invalid bool accepted")
	}
}
