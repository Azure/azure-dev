// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"context"
	"errors"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"azure.ai.connections/internal/exterrors"
	"azure.ai.connections/internal/foundry"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.yaml.in/yaml/v3"
	"google.golang.org/grpc"
)

type connectionAddProjectClientStub struct {
	services             map[string]any
	resources            map[string]any
	rawManifest          map[string]any
	layersFound          bool
	manifestFields       map[string]any
	addRequests          []*azdext.AddServiceRequest
	serviceSetRequests   []*azdext.SetServiceConfigValueRequest
	serviceUnsetRequests []*azdext.UnsetServiceConfigRequest
	mutationCalls        []string
	failAt               int
	failAfterWrite       bool
	writeError           error
}

func (s *connectionAddProjectClientStub) readAuthoringManifest(context.Context) (map[string]any, error) {
	manifest := s.rawManifest
	if manifest == nil {
		manifest = map[string]any{"name": "test", "services": s.services, "resources": s.resources}
		maps.Copy(manifest, s.manifestFields)
	}
	if s.layersFound {
		manifest["layers"] = []any{}
	}
	wire, err := yaml.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	var snapshot map[string]any
	err = yaml.Unmarshal(wire, &snapshot)
	return snapshot, err
}

func (s *connectionAddProjectClientStub) AddService(
	_ context.Context,
	request *azdext.AddServiceRequest,
	_ ...grpc.CallOption,
) (*azdext.EmptyResponse, error) {
	s.addRequests = append(s.addRequests, request)
	s.mutationCalls = append(s.mutationCalls, "add")
	if err := s.mutationError(false); err != nil {
		return nil, err
	}
	service := request.GetService()
	section := service.GetAdditionalProperties().AsMap()
	section["host"] = service.GetHost()
	section["uses"] = stringsToAny(service.GetUses())
	if len(service.GetEnvironment()) > 0 {
		env := map[string]any{}
		for key, value := range service.GetEnvironment() {
			env[key] = strings.ReplaceAll(value, "$", "$$")
		}
		section["env"] = env
	}
	s.services[service.GetName()] = section
	return &azdext.EmptyResponse{}, s.mutationError(true)
}

func (s *connectionAddProjectClientStub) SetServiceConfigValue(
	_ context.Context,
	request *azdext.SetServiceConfigValueRequest,
	_ ...grpc.CallOption,
) (*azdext.EmptyResponse, error) {
	s.serviceSetRequests = append(s.serviceSetRequests, request)
	s.mutationCalls = append(s.mutationCalls, "set:"+request.GetPath())
	if err := s.mutationError(false); err != nil {
		return nil, err
	}
	service, ok := s.services[request.GetServiceName()].(map[string]any)
	if !ok {
		return nil, errors.New("service not found")
	}
	service[request.GetPath()] = request.GetValue().AsInterface()
	return &azdext.EmptyResponse{}, s.mutationError(true)
}

func (s *connectionAddProjectClientStub) UnsetServiceConfig(
	_ context.Context,
	request *azdext.UnsetServiceConfigRequest,
	_ ...grpc.CallOption,
) (*azdext.EmptyResponse, error) {
	s.serviceUnsetRequests = append(s.serviceUnsetRequests, request)
	s.mutationCalls = append(s.mutationCalls, "unset:"+request.GetPath())
	if err := s.mutationError(false); err != nil {
		return nil, err
	}
	service, ok := s.services[request.GetServiceName()].(map[string]any)
	if !ok {
		return nil, errors.New("service not found")
	}
	delete(service, request.GetPath())
	return &azdext.EmptyResponse{}, s.mutationError(true)
}

func (s *connectionAddProjectClientStub) mutationError(afterWrite bool) error {
	if s.failAt == len(s.mutationCalls) && s.failAfterWrite == afterWrite {
		return s.writeError
	}
	return nil
}

func TestLoadConnectionProjectSnapshotSortsProjectKeys(t *testing.T) {
	client := newConnectionAddProjectClientStub()
	client.services["z-project"] = map[string]any{"host": aiProjectServiceHost}
	client.services["a-project"] = map[string]any{"host": aiProjectServiceHost}

	snapshot, err := loadConnectionProjectSnapshot(t.Context(), client, "")
	require.NoError(t, err)
	assert.Equal(t, []string{"a-project", "z-project"}, snapshot.projectKeys)
}

func TestPlanConnectionAddAddsServiceAndAppliesOnce(t *testing.T) {
	root := t.TempDir()
	file := writeConnectionAddDefinition(t, root, "connections/search.yaml", `
category: CognitiveSearch
target: ${SEARCH_ENDPOINT}
authType: ApiKey
credentials:
  key: ${SEARCH_KEY}
metadata:
  optionalRegion: ${OPTIONAL_REGION:-westus}
`)
	client := newConnectionAddProjectClientStub()
	client.services["foundry"] = map[string]any{"host": aiProjectServiceHost}
	client.services["worker.api"] = map[string]any{"host": "containerapp", "language": "js"}

	plan, err := planConnectionAdd(
		t.Context(),
		client,
		root,
		"search.api",
		file,
		false,
		"",
		false,
		nil,
		foundry.DecodeContainedDocument,
	)
	require.NoError(t, err)
	require.Equal(t, "added", plan.mutation)
	require.Equal(t, "./connections/search.yaml", plan.ref)
	require.Equal(t, "foundry", plan.projectKey)
	require.Equal(t, []string{"SEARCH_ENDPOINT", "SEARCH_KEY"}, plan.environmentVariables)
	require.Empty(t, client.mutationCalls)

	require.NotNil(t, plan.serviceSection)
	assert.Equal(
		t,
		map[string]any{"host": "containerapp", "language": "js"},
		client.services["worker.api"],
	)
	service := plan.serviceSection
	assert.Equal(t, aiConnectionHost, service["host"])
	assert.Equal(t, []any{"foundry"}, service["uses"])
	env, ok := service["env"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, map[string]any{
		"SEARCH_ENDPOINT": "${SEARCH_ENDPOINT}",
		"SEARCH_KEY":      "${SEARCH_KEY}",
		"OPTIONAL_REGION": "${OPTIONAL_REGION}",
	}, env)
	assert.Equal(t, "./connections/search.yaml", service["$ref"])

	require.NoError(t, plan.apply(t.Context(), client))
	require.Len(t, client.addRequests, 1)
	assert.Equal(t, []string{"add", "set:env"}, client.mutationCalls)
	added := client.addRequests[0].GetService()
	assert.Equal(t, "search.api", added.GetName())
	assert.Equal(t, aiConnectionHost, added.GetHost())
	assert.Equal(t, []string{"foundry"}, added.GetUses())
	assert.Empty(t, added.GetEnvironment())
	assert.Equal(t, map[string]any{"$ref": "./connections/search.yaml"}, added.GetAdditionalProperties().AsMap())
	assert.Nil(t, added.GetConfig())
	service, ok = client.services["search.api"].(map[string]any)
	require.True(t, ok)
	env, ok = service["env"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, map[string]any{
		"SEARCH_ENDPOINT": "${SEARCH_ENDPOINT}",
		"SEARCH_KEY":      "${SEARCH_KEY}",
		"OPTIONAL_REGION": "${OPTIONAL_REGION}",
	}, env)
}

func TestConnectionAddPreservesUnrelatedChangesAfterPlanning(t *testing.T) {
	root := t.TempDir()
	file := writeConnectionAddDefinition(t, root, "connection.yaml",
		"category: Search\ntarget: https://search.example.test\n")
	client := newConnectionAddProjectClientStub()
	client.services["foundry"] = map[string]any{"host": aiProjectServiceHost}

	plan, err := planConnectionAdd(
		t.Context(), client, root, "search", file, false, "",
		false, nil, foundry.DecodeContainedDocument,
	)
	require.NoError(t, err)

	client.services["new-worker"] = map[string]any{"host": "containerapp"}
	require.NoError(t, plan.apply(t.Context(), client))
	require.Len(t, client.addRequests, 1)
	assert.Contains(t, client.services, "new-worker")
}

func TestPlanConnectionAddRejectsDefinitionChangedDuringProjectSelection(t *testing.T) {
	for _, test := range []struct {
		name   string
		before string
		after  string
	}{
		{"definition", "https://old.example.test", "https://new.example.test"},
		{"required environment reference", "${OLD_KEY}", "${NEW_KEY}"},
		{"optional environment reference", "${OLD_KEY:-fallback}", "${NEW_KEY:-fallback}"},
	} {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			file := writeConnectionAddDefinition(t, root, "connection.yaml",
				"category: Search\ntarget: "+test.before+"\n")
			client := newConnectionAddProjectClientStub()
			client.services["first"] = map[string]any{"host": aiProjectServiceHost}
			client.services["second"] = map[string]any{"host": aiProjectServiceHost}
			before, err := client.readAuthoringManifest(t.Context())
			require.NoError(t, err)
			selected := false
			selectProject := func(_ context.Context, keys []string) (string, error) {
				selected = true
				require.Equal(t, []string{"first", "second"}, keys)
				writeConnectionAddDefinition(t, root, "connection.yaml",
					"category: Search\ntarget: "+test.after+"\n")
				return "first", nil
			}

			plan, err := planConnectionAdd(t.Context(), client, root, "search",
				file, false, "", false, selectProject, nil)
			require.True(t, selected)
			localErr := requireConnectionValidationError(t, err, exterrors.CodeProjectChanged, "definition changed")
			assert.Contains(t, localErr.Message, "no changes were written")
			assert.Contains(t, localErr.Suggestion, "Rerun the command")
			assert.Nil(t, plan)
			assert.Empty(t, client.mutationCalls)
			after, err := client.readAuthoringManifest(t.Context())
			require.NoError(t, err)
			assert.Equal(t, before, after)
		})
	}
}

func TestConnectionAuthoringLocalSnapshotPreservesLargeIntegers(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(map[bool]string{false: "added", true: "updated"}[existing], func(t *testing.T) {
			root := t.TempDir()
			nested := filepath.Join(root, "nested")
			require.NoError(t, os.MkdirAll(nested, 0o700))
			t.Chdir(nested)
			file := writeConnectionAddDefinition(t, root, "connections/search.yaml",
				"category: Search\ntarget: ${SEARCH_ENDPOINT}\n")
			client := newConnectionAddProjectClientStub()
			client.services["foundry"] = map[string]any{"host": aiProjectServiceHost}
			client.services["worker.api"] = map[string]any{
				"host": "containerapp", "language": "js",
				"config": map[string]any{"number": uint64(9007199254740993)},
			}
			if existing {
				client.services["search-api"] = map[string]any{
					"host": aiConnectionHost, "uses": []any{"foundry"},
					"category": "Search", "target": "old",
					"config": map[string]any{"number": uint64(9007199254740993)},
				}
			}
			manifest, err := client.readAuthoringManifest(t.Context())
			require.NoError(t, err)
			wire, err := yaml.Marshal(manifest)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(root, "azure.yaml"), wire, 0o600))
			// Hide the stub-only seam to exercise the
			// contained disk reader.
			diskClient := struct{ connectionAddProjectClient }{client}
			plan, err := planConnectionAdd(t.Context(), diskClient, root, "search-api", file,
				false, "", true, nil, nil)
			require.NoError(t, err)
			encoded, err := yaml.Marshal(plan.serviceSection)
			require.NoError(t, err)
			if existing {
				assert.Contains(t, string(encoded), "9007199254740993")
			}
			assert.NotContains(t, string(encoded), "9007199254740992")
			require.NoError(t, plan.apply(t.Context(), client))
			service, ok := client.services["search-api"].(map[string]any)
			require.True(t, ok)
			env, ok := service["env"].(map[string]any)
			require.True(t, ok)
			assert.Equal(t, "${SEARCH_ENDPOINT}", env["SEARCH_ENDPOINT"])
			if existing {
				config, ok := service["config"].(map[string]any)
				require.True(t, ok)
				assert.Equal(t, uint64(9007199254740993), config["number"])
				for _, request := range client.serviceSetRequests {
					assert.NotEqual(t, "config", request.GetPath())
				}
			} else {
				assert.Equal(t, []string{"add", "set:env"}, client.mutationCalls)
			}
			assert.Equal(t, map[string]any{
				"host": "containerapp", "language": "js",
				"config": map[string]any{"number": uint64(9007199254740993)},
			}, client.services["worker.api"])
		})
	}
}

func TestConnectionAuthoringResolvesActualManifest(t *testing.T) {
	tests := []struct {
		name       string
		parentYAML bool
		childYAML  bool
		childYML   bool
		wantOwner  string
	}{
		{name: "standalone yml", childYML: true, wantOwner: "yml-owner"},
		{name: "child yml before parent yaml", parentYAML: true, childYML: true, wantOwner: "yml-owner"},
		{name: "yaml precedence", parentYAML: true, childYAML: true, childYML: true, wantOwner: "yaml-owner"},
		{name: "yaml only", childYAML: true, wantOwner: "yaml-owner"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			parent := t.TempDir()
			root := filepath.Join(parent, "child")
			nested := filepath.Join(root, "nested")
			require.NoError(t, os.MkdirAll(nested, 0o700))
			for name, enabled := range map[string]bool{"azure.yaml": test.childYAML, "azure.yml": test.childYML} {
				if enabled {
					owner := map[string]string{"azure.yaml": "yaml-owner", "azure.yml": "yml-owner"}[name]
					manifest := "name: child\nservices:\n  " + owner + ":\n    host: azure.ai.project\n"
					require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte(manifest), 0o600))
				}
			}
			parentManifest := []byte("name: parent\nservices:\n  parent-owner:\n    host: azure.ai.project\n")
			if test.parentYAML {
				require.NoError(t, os.WriteFile(filepath.Join(parent, "azure.yaml"), parentManifest, 0o600))
			}
			t.Chdir(nested)
			t.Setenv("AZD_EXEC_PROJECT_DIR", parent)
			projectRoot, err := connectionAuthoringProjectDir()
			require.NoError(t, err)
			require.Equal(t, root, projectRoot)
			file := writeConnectionAddDefinition(t, root, "connections/search.yaml",
				"category: Search\ntarget: https://search.example.test\n")
			diskClient := struct{ connectionAddProjectClient }{newConnectionAddProjectClientStub()}
			plan, err := planConnectionAdd(t.Context(), diskClient, projectRoot, "search", file,
				false, "", true, nil, nil)
			require.NoError(t, err)
			assert.Equal(t, test.wantOwner, plan.projectKey)
			assert.Equal(t, "./connections/search.yaml", plan.ref)
			assert.Equal(t, []string{test.wantOwner}, plan.addRequest.GetService().GetUses())
			if test.parentYAML {
				// #nosec G304 -- Reads this test's temporary fixture.
				actual, err := os.ReadFile(filepath.Join(parent, "azure.yaml"))
				require.NoError(t, err)
				assert.Equal(t, parentManifest, actual)
			}
		})
	}
}

func TestConnectionAuthoringDoesNotReadParentManifestForExplicitRoot(t *testing.T) {
	parent := t.TempDir()
	root := filepath.Join(parent, "child")
	require.NoError(t, os.Mkdir(root, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(parent, "azure.yaml"),
		[]byte("name: parent\nservices: {}\n"), 0o600))
	diskClient := struct{ connectionAddProjectClient }{newConnectionAddProjectClientStub()}
	_, err := loadConnectionProjectSnapshot(t.Context(), diskClient, root)
	require.ErrorContains(t, err, "does not contain a project manifest")
}

func TestConnectionAuthoringUpdatesLeaveUnownedChangesOnHost(t *testing.T) {
	root := t.TempDir()
	file := writeConnectionAddDefinition(t, root, "connection.yaml",
		"category: Search\ntarget: https://search.example.test\n")
	client := newConnectionAddProjectClientStub()
	client.services["foundry"] = map[string]any{"host": aiProjectServiceHost}
	client.services["search"] = map[string]any{
		"host": aiConnectionHost, "uses": []any{"foundry"}, "category": "Search", "target": "old",
	}
	plan, err := planConnectionAdd(t.Context(), client, root, "search", file,
		false, "", true, nil, nil)
	require.NoError(t, err)
	search, ok := client.services["search"].(map[string]any)
	require.True(t, ok)
	search["config"] = map[string]any{"large": uint64(9007199254740993)}
	require.NoError(t, plan.apply(t.Context(), client))
	assert.Equal(t, map[string]any{"large": uint64(9007199254740993)}, search["config"])
	assert.Equal(t, "./connection.yaml", search["$ref"])
	assert.NotContains(t, search, "target")
	assert.Empty(t, client.addRequests)
}

func TestPlanConnectionAddResolvesDefinitionRefsRelativeToFile(t *testing.T) {
	root := t.TempDir()
	file := writeConnectionAddDefinition(t, root, "definitions/search.yaml", `
$ref: ./details.yaml
`)
	writeConnectionAddDefinition(t, root, "definitions/details.yaml", `
category: CognitiveSearch
target: ${SEARCH_ENDPOINT}
authType: ApiKey
credentials:
  key: ${SEARCH_KEY}
`)
	client := newConnectionAddProjectClientStub()
	client.services["foundry"] = map[string]any{"host": aiProjectServiceHost}

	plan, err := planConnectionAdd(
		t.Context(),
		client,
		root,
		"search",
		file,
		false,
		"",
		false,
		nil,
		foundry.DecodeContainedDocument,
	)
	require.NoError(t, err)
	assert.Equal(t, "./definitions/search.yaml", plan.ref)
	assert.Equal(t, []string{"SEARCH_ENDPOINT", "SEARCH_KEY"}, plan.environmentVariables)
	assert.Equal(t, "added", plan.mutation)
}

func TestPlanConnectionAddUpdatesInlineDefinitionAndPreservesCoreSettings(t *testing.T) {
	root := t.TempDir()
	file := writeConnectionAddDefinition(t, root, "connections/search.yaml", `
category: CognitiveSearch
target: https://search.example.test
authType: None
metadata:
  endpoint: ${SEARCH_METADATA}
`)
	client := newConnectionAddProjectClientStub()
	client.resources["cache"] = map[string]any{"type": "local"}
	client.services["foundry"] = map[string]any{"host": aiProjectServiceHost}
	client.services["worker"] = map[string]any{"host": "containerapp", "language": "js"}
	client.services["search"] = map[string]any{
		"host":        aiConnectionHost,
		"name":        "old-resource-name",
		"$schema":     "old-schema",
		"category":    "OldCategory",
		"target":      "old-target",
		"authType":    "ApiKey",
		"credentials": map[string]any{"key": "old-secret"},
		"uses":        []any{"cache", "foundry", "worker"},
		"env": map[string]any{
			"STALE": "old-template",
			"KEEP":  "literal",
		},
		"hooks":    map[string]any{"predeploy": []any{map[string]any{"run": "echo ready"}}},
		"language": "python",
		"infra":    map[string]any{"path": "infra"},
		"config":   map[string]any{"keep": "core-config"},
	}

	plan, err := planConnectionAdd(
		t.Context(),
		client,
		root,
		"search",
		file,
		false,
		"",
		false,
		nil,
		foundry.DecodeContainedDocument,
	)
	require.NoError(t, err)
	require.Equal(t, "updated", plan.mutation)
	assert.Equal(t, []string{"SEARCH_METADATA"}, plan.environmentVariables)
	require.NotNil(t, plan.serviceSection)
	candidate := plan.serviceSection

	assert.Equal(t, aiConnectionHost, candidate["host"])
	assert.Equal(t, "./connections/search.yaml", candidate["$ref"])
	assert.Equal(t, []any{"cache", "foundry", "worker"}, candidate["uses"])
	assert.Equal(t, "python", candidate["language"])
	assert.Equal(t, map[string]any{"predeploy": []any{map[string]any{"run": "echo ready"}}}, candidate["hooks"])
	assert.Equal(t, map[string]any{"path": "infra"}, candidate["infra"])
	assert.Equal(t, map[string]any{"keep": "core-config"}, candidate["config"])
	assert.Equal(t, map[string]any{
		"STALE":           "old-template",
		"KEEP":            "literal",
		"SEARCH_METADATA": "${SEARCH_METADATA}",
	}, candidate["env"])
	for _, field := range []string{
		"name", "$schema", "category", "target", "authType", "credentials",
	} {
		assert.NotContains(t, candidate, field)
	}
	require.Empty(t, client.mutationCalls)

	require.NoError(t, plan.apply(t.Context(), client))
	assert.Empty(t, client.addRequests)
	assert.Equal(t, []string{
		"set:$ref", "set:env",
		"unset:$schema", "unset:authType", "unset:category",
		"unset:credentials", "unset:name", "unset:target",
	}, client.mutationCalls)
	assert.Equal(t, plan.serviceSection, client.services["search"])
}

func TestPlanConnectionAddReplacesLegacyConfigDefinition(t *testing.T) {
	root := t.TempDir()
	file := writeConnectionAddDefinition(t, root, "definitions/search.json", `{
  "category": "CognitiveSearch",
  "target": "https://search.example.test",
  "authType": "None"
}`)
	client := newConnectionAddProjectClientStub()
	client.services["foundry"] = map[string]any{"host": aiProjectServiceHost}
	client.services["search"] = map[string]any{
		"host": aiConnectionHost,
		"uses": []any{"foundry"},
		"config": map[string]any{
			"category": "OldCategory",
			"target":   "old-target",
			"authType": "None",
		},
	}

	plan, err := planConnectionAdd(
		t.Context(),
		client,
		root,
		"search",
		file,
		false,
		"",
		false,
		nil,
		foundry.DecodeContainedDocument,
	)
	require.NoError(t, err)
	require.Equal(t, "updated", plan.mutation)
	candidate := plan.serviceSection
	assert.NotContains(t, candidate, "config")
	assert.Equal(t, "./definitions/search.json", candidate["$ref"])
	assert.Equal(t, []any{"foundry"}, candidate["uses"])
	require.NoError(t, plan.apply(t.Context(), client))
	assert.Equal(t, []string{"unset:config", "set:$ref"}, client.mutationCalls)
	assert.Equal(t, candidate, client.services["search"])
}

func TestPlanConnectionAddLeavesUnchangedServiceUntouched(t *testing.T) {
	root := t.TempDir()
	file := writeConnectionAddDefinition(t, root, "connections/search.yaml", `
category: CognitiveSearch
target: https://search.example.test
authType: ApiKey
credentials:
  key: ${SEARCH_KEY}
metadata:
  optionalRegion: ${OPTIONAL_REGION:-westus}
`)
	client := newConnectionAddProjectClientStub()
	client.services["foundry"] = map[string]any{"host": aiProjectServiceHost}
	client.services["search"] = map[string]any{
		"host": aiConnectionHost,
		"$ref": "./connections/search.yaml",
		"uses": []any{"foundry"},
		"env": map[string]any{
			"SEARCH_KEY":      "${SEARCH_KEY}",
			"OPTIONAL_REGION": "custom",
			"STALE":           "preserve",
		},
		"hooks": map[string]any{"postdeploy": []any{map[string]any{"run": "echo done"}}},
	}

	plan, err := planConnectionAdd(
		t.Context(),
		client,
		root,
		"search",
		file,
		false,
		"",
		false,
		nil,
		foundry.DecodeContainedDocument,
	)
	require.NoError(t, err)
	require.Equal(t, "unchanged", plan.mutation)
	assert.Equal(t, []string{"SEARCH_KEY"}, plan.environmentVariables)
	require.NoError(t, plan.apply(t.Context(), client))
	assert.Empty(t, client.mutationCalls)
}

func TestPlanConnectionAddRejectsInvalidProjectStateBeforeMutation(t *testing.T) {
	tests := []struct {
		name          string
		fileContent   string
		setup         func(*connectionAddProjectClientStub)
		projectKey    string
		projectSet    bool
		wantErrText   string
		errNotContain string
	}{
		{
			name:        "layered manifest",
			fileContent: "category: Search\ntarget: https://search.example.test\n",
			setup: func(client *connectionAddProjectClientStub) {
				client.layersFound = true
			},
			wantErrText: "layered manifests",
		},
		{
			name:        "occupied service key",
			fileContent: "category: Search\ntarget: https://search.example.test\n",
			setup: func(client *connectionAddProjectClientStub) {
				client.services["search"] = map[string]any{"host": "containerapp"}
			},
			wantErrText: "different service host",
		},
		{
			name:        "malformed uses list",
			fileContent: "category: Search\ntarget: https://search.example.test\n",
			setup: func(client *connectionAddProjectClientStub) {
				client.services["foundry"] = map[string]any{"host": aiProjectServiceHost}
				client.services["search"] = map[string]any{
					"host": aiConnectionHost,
					"uses": "foundry",
				}
			},
			wantErrText: "uses must be a list",
		},
		{
			name:        "dangling dependency",
			fileContent: "category: Search\ntarget: https://search.example.test\n",
			setup: func(client *connectionAddProjectClientStub) {
				client.services["foundry"] = map[string]any{"host": aiProjectServiceHost}
				client.services["search"] = map[string]any{
					"host": aiConnectionHost,
					"uses": []any{"deleted-service"},
				}
			},
			wantErrText: "does not exist",
		},
		{
			name: "conflicting env binding is not disclosed",
			fileContent: `category: Search
target: https://search.example.test
authType: ApiKey
credentials:
  key: ${API_KEY}
`,
			setup: func(client *connectionAddProjectClientStub) {
				client.services["foundry"] = map[string]any{"host": aiProjectServiceHost}
				client.services["search"] = map[string]any{
					"host": aiConnectionHost,
					"uses": []any{"foundry"},
					"env":  map[string]any{"API_KEY": "private-binding"},
				}
			},
			wantErrText:   "API_KEY",
			errNotContain: "private-binding",
		},
		{
			name:        "unowned env value is not coerced through Struct",
			fileContent: "category: Search\ntarget: https://search.example.test\n",
			setup: func(client *connectionAddProjectClientStub) {
				client.services["foundry"] = map[string]any{"host": aiProjectServiceHost}
				client.services["search"] = map[string]any{
					"host": aiConnectionHost,
					"uses": []any{"foundry"},
					"env":  map[string]any{"KEEP": uint64(9007199254740993)},
				}
			},
			wantErrText:   "must be a string",
			errNotContain: "9007199254740993",
		},
		{
			name:        "invalid explicit project",
			fileContent: "category: Search\ntarget: https://search.example.test\n",
			setup: func(client *connectionAddProjectClientStub) {
				client.services["foundry"] = map[string]any{"host": aiProjectServiceHost}
			},
			projectKey:  "worker",
			projectSet:  true,
			wantErrText: "does not identify an existing",
		},
		{
			name: "case-insensitive name collision",
			fileContent: `name: shared-name
category: Search
target: https://search.example.test
`,
			setup: func(client *connectionAddProjectClientStub) {
				client.services["foundry"] = map[string]any{"host": aiProjectServiceHost}
				client.services["other"] = map[string]any{
					"host":     aiConnectionHost,
					"name":     "SHARED-NAME",
					"category": "Search",
					"target":   "https://other.example.test",
				}
			},
			wantErrText: "same Connection name",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			root := t.TempDir()
			file := writeConnectionAddDefinition(t, root, "connection.yaml", test.fileContent)
			client := newConnectionAddProjectClientStub()
			test.setup(client)

			_, err := planConnectionAdd(
				t.Context(),
				client,
				root,
				"search",
				file,
				test.projectSet,
				test.projectKey,
				true,
				nil,
				foundry.DecodeContainedDocument,
			)
			require.Error(t, err)
			assert.Contains(t, err.Error(), test.wantErrText)
			if test.errNotContain != "" {
				assert.NotContains(t, err.Error(), test.errNotContain)
			}
			assert.Empty(t, client.mutationCalls)
		})
	}
}

func TestChooseConnectionProject(t *testing.T) {
	tests := []struct {
		name              string
		projectKeys       []string
		owners            []string
		existing          bool
		explicit          bool
		requested         string
		noPrompt          bool
		selectResult      string
		selectError       error
		want              string
		wantError         string
		wantSelectCalls   int
		wantSelectProject []string
	}{
		{
			name:      "new service requires a Project",
			wantError: "no azure.ai.project service exists",
		},
		{
			name:        "new service selects its only Project",
			projectKeys: []string{"project"},
			want:        "project",
		},
		{
			name:              "new service prompts among sorted Projects",
			projectKeys:       []string{"a-project", "z-project"},
			selectResult:      "a-project",
			want:              "a-project",
			wantSelectCalls:   1,
			wantSelectProject: []string{"a-project", "z-project"},
		},
		{
			name:        "no prompt requires explicit selection",
			projectKeys: []string{"a-project", "z-project"},
			noPrompt:    true,
			wantError:   "--project is required in no-prompt mode",
		},
		{
			name:        "existing service with no owner requires repair",
			projectKeys: []string{"project"},
			existing:    true,
			wantError:   "does not have one unambiguous",
		},
		{
			name:        "existing unique owner is preserved",
			projectKeys: []string{"project", "other"},
			owners:      []string{"project"},
			existing:    true,
			want:        "project",
		},
		{
			name:        "existing multiple owners require repair",
			projectKeys: []string{"project", "other"},
			owners:      []string{"project", "other"},
			existing:    true,
			wantError:   "does not have one unambiguous",
		},
		{
			name:        "explicit Project repairs missing owner",
			projectKeys: []string{"project"},
			existing:    true,
			explicit:    true,
			requested:   "project",
			want:        "project",
		},
		{
			name:        "explicit Project replaces multiple owners",
			projectKeys: []string{"project", "other", "selected"},
			owners:      []string{"project", "other"},
			existing:    true,
			explicit:    true,
			requested:   "selected",
			want:        "selected",
		},
		{
			name:        "invalid explicit Project never prompts",
			projectKeys: []string{"project"},
			explicit:    true,
			requested:   "worker",
			wantError:   "does not identify an existing",
		},
		{
			name:        "explicit blank Project is rejected",
			projectKeys: []string{"project"},
			explicit:    true,
			requested:   " ",
			wantError:   "must name an existing",
		},
		{
			name:            "prompt cancellation is propagated",
			projectKeys:     []string{"one", "two"},
			selectError:     errors.New("cancelled"),
			wantError:       "cancelled",
			wantSelectCalls: 1,
		},
		{
			name:            "invalid prompt selection is rejected",
			projectKeys:     []string{"one", "two"},
			selectResult:    "unknown",
			wantError:       "does not identify an existing",
			wantSelectCalls: 1,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			owners := map[string]struct{}{}
			for _, key := range test.owners {
				owners[key] = struct{}{}
			}
			selectCalls := 0
			selected, err := chooseConnectionProject(
				t.Context(),
				test.projectKeys,
				owners,
				test.existing,
				test.explicit,
				test.requested,
				test.noPrompt,
				func(_ context.Context, keys []string) (string, error) {
					selectCalls++
					if test.wantSelectProject != nil {
						assert.Equal(t, test.wantSelectProject, keys)
					}
					return test.selectResult, test.selectError
				},
			)
			if test.wantError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), test.wantError)
			} else {
				require.NoError(t, err)
				assert.Equal(t, test.want, selected)
			}
			assert.Equal(t, test.wantSelectCalls, selectCalls)
		})
	}
}

func TestReconcileConnectionUses(t *testing.T) {
	tests := []struct {
		name        string
		uses        []string
		projectKeys []string
		selected    string
		want        []string
	}{
		{
			name:        "replace owners at first owner position and retain duplicates",
			uses:        []string{"worker", "old-project", "cache", "old-project", "foundry"},
			projectKeys: []string{"old-project", "foundry", "new-project"},
			selected:    "new-project",
			want:        []string{"worker", "new-project", "cache"},
		},
		{
			name:     "append owner when none exists",
			uses:     []string{"worker", "cache"},
			selected: "foundry",
			want:     []string{"worker", "cache", "foundry"},
		},
		{
			name:        "collapse duplicate same-owner references",
			uses:        []string{"foundry", "worker", "foundry"},
			projectKeys: []string{"foundry"},
			selected:    "foundry",
			want:        []string{"foundry", "worker"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, reconcileConnectionUses(
				test.uses,
				test.projectKeys,
				test.selected,
			))
		})
	}
}

func TestParseConnectionUses(t *testing.T) {
	tests := []struct {
		name    string
		service map[string]any
		want    []string
		wantErr bool
	}{
		{name: "missing", service: map[string]any{}},
		{name: "null", service: map[string]any{"uses": nil}},
		{name: "empty", service: map[string]any{"uses": []any{}}, want: []string{}},
		{name: "strings", service: map[string]any{"uses": []any{"project", "cache"}}, want: []string{"project", "cache"}},
		{name: "non-list", service: map[string]any{"uses": "project"}, wantErr: true},
		{name: "non-string item", service: map[string]any{"uses": []any{"project", 5}}, wantErr: true},
		{name: "blank item", service: map[string]any{"uses": []any{" "}}, wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseConnectionUses(test.service)
			if test.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.want, got)
		})
	}
}

func TestValidateConnectionDependencies(t *testing.T) {
	snapshot := &connectionProjectSnapshot{
		services: map[string]any{
			"project": map[string]any{"host": aiProjectServiceHost},
			"worker":  map[string]any{"host": "containerapp"},
			"broken":  "not-a-service",
		},
		resources: map[string]any{"cache": map[string]any{"type": "local"}},
	}

	tests := []struct {
		name       string
		uses       []string
		wantOwners []string
		wantError  string
	}{
		{name: "project service is owner", uses: []string{"project"}, wantOwners: []string{"project"}},
		{name: "service and resource dependencies are preserved", uses: []string{"worker", "cache"}},
		{name: "dangling dependency is rejected", uses: []string{"missing"}, wantError: "does not exist"},
		{name: "malformed referenced service is rejected", uses: []string{"broken"}, wantError: "not a valid service"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			owners, err := validateConnectionDependencies(test.uses, snapshot)
			if test.wantError != "" {
				require.Error(t, err)
				assert.Contains(t, err.Error(), test.wantError)
				return
			}
			require.NoError(t, err)
			keys := make([]string, 0, len(owners))
			for key := range owners {
				keys = append(keys, key)
			}
			assert.ElementsMatch(t, test.wantOwners, keys)
		})
	}
}

func TestConnectionReferenceMergingRejectsConflictsWithoutValues(t *testing.T) {
	env, include, err := mergeConnectionEnvironment(
		map[string]any{
			"env": map[string]any{
				"API_KEY": "sensitive-value",
				"STALE":   "keep",
			},
		},
		[]string{"API_KEY"},
		[]string{"API_KEY"},
	)
	require.Error(t, err)
	assert.False(t, include)
	assert.Nil(t, env)
	assert.Contains(t, err.Error(), "API_KEY")
	assert.NotContains(t, err.Error(), "sensitive-value")
}

func TestConnectionDefinitionSourceUsesLegacyConfigOnlyWhenNeeded(t *testing.T) {
	tests := []struct {
		name       string
		service    map[string]any
		wantConfig bool
		wantSource map[string]any
	}{
		{
			name: "inline connection fields take precedence",
			service: map[string]any{
				"host":     aiConnectionHost,
				"category": "Search",
				"config":   map[string]any{"category": "Ignored"},
			},
			wantSource: map[string]any{"category": "Search"},
		},
		{
			name: "legacy config is used when no extension fields exist",
			service: map[string]any{
				"host": aiConnectionHost,
				"config": map[string]any{
					"category": "Search",
					"target":   "https://search.example.test",
				},
			},
			wantConfig: true,
			wantSource: map[string]any{
				"category": "Search",
				"target":   "https://search.example.test",
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			source, fromConfig := connectionDefinitionSource(test.service)
			assert.Equal(t, test.wantConfig, fromConfig)
			assert.Equal(t, test.wantSource, source)
		})
	}
}

func newConnectionAddProjectClientStub() *connectionAddProjectClientStub {
	return &connectionAddProjectClientStub{
		services:  map[string]any{},
		resources: map[string]any{},
	}
}

func writeConnectionAddDefinition(t *testing.T, root, relativePath, content string) string {
	t.Helper()
	path := filepath.Join(root, filepath.FromSlash(relativePath))
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}
