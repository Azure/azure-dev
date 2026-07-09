// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package azure

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/stretchr/testify/require"
)

// ---- roundtrip test harness ----
//
// Mirrors the pattern used by azure.ai.agents' copy of this client
// (internal/pkg/azure/foundry_toolsets_client_test.go): a custom
// http.RoundTripper intercepts requests before any real network I/O, so the
// real production pipeline (URL construction, JSON marshaling/unmarshaling,
// status-code handling, pagination) runs exactly as it would against the
// live service, without touching the network.

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func newTestPipeline(fn roundTripFunc) runtime.Pipeline {
	return runtime.NewPipeline(
		"test",
		"v0.0.0",
		runtime.PipelineOptions{},
		&policy.ClientOptions{
			Transport: &http.Client{Transport: fn},
		},
	)
}

// newTestToolboxClient creates a FoundryToolboxClient backed by a custom HTTP
// round-tripper so we can exercise the real client end-to-end (real JSON
// wire format, real URL building) without touching the network.
func newTestToolboxClient(endpoint string, fn roundTripFunc) *FoundryToolboxClient {
	return &FoundryToolboxClient{
		endpoint: endpoint,
		pipeline: newTestPipeline(fn),
	}
}

func jsonResponse(status int, v any) *http.Response {
	body, _ := json.Marshal(v)
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(bytes.NewReader(body)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
	}
}

func notFoundResponse() *http.Response {
	return &http.Response{
		StatusCode: http.StatusNotFound,
		Body:       io.NopCloser(strings.NewReader(`{"error":{"code":"NotFound"}}`)),
		Header:     make(http.Header),
	}
}

// ---- fake Foundry Toolboxes v1 service ----
//
// A small, stateful, in-memory stand-in for the real data-plane API. It is
// faithful to the semantics that matter for azure-dev#9034: versions are
// immutable and numbered sequentially, and creating a new version NEVER
// advances default_version (only a PATCH to the toolbox does that — see
// toolbox_publish.go / SetDefaultVersion). This lets a test prove, over the
// real HTTP/JSON wire format, that "read latest -> merge -> create" behaves
// the way the fixed command layer (resolveBaseVersion) expects.
type fakeToolboxService struct {
	mu             sync.Mutex
	name           string
	defaultVersion string
	nextVersion    int
	versions       map[string]ToolboxVersionObject
}

func newFakeToolboxService(name string, initial ToolboxVersionObject) *fakeToolboxService {
	initial.Name = name
	initial.Version = "1"
	return &fakeToolboxService{
		name:           name,
		defaultVersion: "1",
		nextVersion:    1,
		versions:       map[string]ToolboxVersionObject{"1": initial},
	}
}

func (f *fakeToolboxService) handle(req *http.Request) (*http.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	// The real endpoint has its own path prefix (e.g.
	// "/api/projects/agent-with-toolbox-dev"), so find the "toolboxes"
	// segment rather than assuming it's the first path element.
	all := strings.Split(strings.Trim(req.URL.Path, "/"), "/")
	idx := -1
	for i, seg := range all {
		if seg == "toolboxes" {
			idx = i
			break
		}
	}
	if idx == -1 {
		return notFoundResponse(), nil
	}
	parts := all[idx:]
	// parts[0] == "toolboxes", parts[1] == name, optionally
	// parts[2] == "versions", optionally parts[3] == version.

	switch {
	case req.Method == http.MethodGet && len(parts) == 2:
		// GET /toolboxes/{name}
		return jsonResponse(http.StatusOK, ToolboxObject{
			ID: f.name, Name: f.name, DefaultVersion: f.defaultVersion,
		}), nil

	case req.Method == http.MethodGet && len(parts) == 3 && parts[2] == "versions":
		// GET /toolboxes/{name}/versions (list, single page)
		list := make([]ToolboxVersionObject, 0, len(f.versions))
		for _, v := range f.versions {
			list = append(list, v)
		}
		sort.Slice(list, func(i, j int) bool {
			vi, _ := strconv.Atoi(list[i].Version)
			vj, _ := strconv.Atoi(list[j].Version)
			return vi < vj
		})
		return jsonResponse(http.StatusOK, struct {
			Data    []ToolboxVersionObject `json:"data"`
			HasMore bool                   `json:"has_more,omitempty"`
		}{Data: list}), nil

	case req.Method == http.MethodGet && len(parts) == 4 && parts[2] == "versions":
		// GET /toolboxes/{name}/versions/{version}
		v, ok := f.versions[parts[3]]
		if !ok {
			return notFoundResponse(), nil
		}
		return jsonResponse(http.StatusOK, v), nil

	case req.Method == http.MethodPost && len(parts) == 3 && parts[2] == "versions":
		// POST /toolboxes/{name}/versions (create a new immutable version)
		raw, _ := io.ReadAll(req.Body)
		var body CreateToolboxVersionRequest
		_ = json.Unmarshal(raw, &body)

		f.nextVersion++
		newVersion := strconv.Itoa(f.nextVersion)
		created := ToolboxVersionObject{
			ID: f.name, Name: f.name, Version: newVersion,
			Description: body.Description, Metadata: body.Metadata,
			Tools: body.Tools, Skills: body.Skills, Policies: body.Policies,
		}
		f.versions[newVersion] = created
		// default_version is deliberately NOT advanced here — matches the
		// real service post-PR#8396 (mutation verbs no longer auto-promote).
		return jsonResponse(http.StatusCreated, created), nil

	case req.Method == http.MethodPatch && len(parts) == 2:
		// PATCH /toolboxes/{name} (azd ai toolbox publish)
		raw, _ := io.ReadAll(req.Body)
		var body struct {
			DefaultVersion string `json:"default_version"`
		}
		_ = json.Unmarshal(raw, &body)
		f.defaultVersion = body.DefaultVersion
		return jsonResponse(http.StatusOK, ToolboxObject{
			ID: f.name, Name: f.name, DefaultVersion: f.defaultVersion,
		}), nil
	}

	return notFoundResponse(), nil
}

// TestFoundryToolboxClient_Live_SkillAddChainsFromLatest_Regression9034 is an
// HTTP-wire-level regression test for azure-dev#9034, using the exact repro
// from the issue (toolbox/skill names included). It drives the REAL
// production FoundryToolboxClient — real JSON marshaling/unmarshaling, real
// URL construction, real status-code handling — against a stateful fake
// service, replicating exactly what the fixed command layer
// (resolveBaseVersion + runSkillAddWith) does: read the toolbox, list
// versions, pick the numerically latest, fetch it, merge in the new skill,
// and create a new version.
//
// This closes a real coverage gap: internal/cmd's own tests exercise this
// logic exclusively through the toolboxClient mock interface, which never
// touches JSON serialization or HTTP semantics at all. This test proves the
// real client correctly round-trips that data end-to-end.
func TestFoundryToolboxClient_Live_SkillAddChainsFromLatest_Regression9034(t *testing.T) {
	const toolboxName = "web-search-toolbox"

	svc := newFakeToolboxService(toolboxName, ToolboxVersionObject{
		Description: "web search toolbox",
		Tools:       []map[string]any{{"type": "web_search", "name": "web_search"}},
		Skills:      []map[string]any{},
	})
	client := newTestToolboxClient(
		"https://example.services.ai.azure.com/api/projects/agent-with-toolbox-dev",
		roundTripFunc(svc.handle),
	)
	ctx := context.Background()

	// pickLatest mirrors internal/cmd's latestToolboxVersion: numerically
	// highest version wins.
	pickLatest := func(versions []ToolboxVersionObject) string {
		latest := versions[0].Version
		latestNum, _ := strconv.Atoi(latest)
		for _, v := range versions[1:] {
			n, _ := strconv.Atoi(v.Version)
			if n > latestNum {
				latest, latestNum = v.Version, n
			}
		}
		return latest
	}

	// addSkill replicates runSkillAddWith's post-fix logic: branch from the
	// latest version (not the toolbox's default_version), merge in the new
	// skill, and create a new immutable version.
	addSkill := func(skillName string) (newVersion, baseVersion string) {
		versions, err := client.ListToolboxVersions(ctx, toolboxName)
		require.NoError(t, err)
		base := pickLatest(versions)

		current, err := client.GetToolboxVersion(ctx, toolboxName, base)
		require.NoError(t, err)

		newSkills := append(append([]map[string]any{}, current.Skills...), map[string]any{
			"type": "skill_reference", "name": skillName,
		})
		created, err := client.CreateToolboxVersion(ctx, toolboxName, &CreateToolboxVersionRequest{
			Description: current.Description,
			Metadata:    current.Metadata,
			Tools:       current.Tools,
			Skills:      newSkills,
		})
		require.NoError(t, err)
		return created.Version, base
	}

	// Step 1 (issue repro): skill add web-search-toolbox greeting-test-2606
	v2, base1 := addSkill("greeting-test-2606")
	require.Equal(t, "2", v2, "first add creates version 2")
	require.Equal(t, "1", base1, "first add has only version 1 to branch from")

	// Step 2 (issue repro): skill add web-search-toolbox code-review-test-2606
	v3, base2 := addSkill("code-review-test-2606")
	require.Equal(t, "3", v3, "second add creates version 3")
	require.Equal(t, "2", base2,
		"regression azure-dev#9034: second add must branch from latest (v2), not the stale default (v1)")

	// Step 3 (issue repro): toolbox show web-search-toolbox --version 3
	shown, err := client.GetToolboxVersion(ctx, toolboxName, "3")
	require.NoError(t, err)
	names := make([]string, 0, len(shown.Skills))
	for _, s := range shown.Skills {
		names = append(names, s["name"].(string))
	}
	require.ElementsMatch(t, []string{"greeting-test-2606", "code-review-test-2606"}, names,
		"v3 must carry forward BOTH skills — this is the exact assertion the "+
			"GitHub issue reports failing")

	// The toolbox's default version must remain untouched (publish is a
	// separate, deliberate user gesture — no auto-promotion).
	tb, err := client.GetToolbox(ctx, toolboxName)
	require.NoError(t, err)
	require.Equal(t, "1", tb.DefaultVersion, "default version must remain unchanged")
}

// TestFoundryToolboxClient_Live_PreFixBehavior_WouldDropSkill_Regression9034
// demonstrates, over the same real wire-level client, that branching from
// the toolbox's *default* version (the pre-fix behavior) reproduces the
// issue exactly: the second add's result is missing the first add's skill.
// This is a characterization test of the OLD behavior, kept side-by-side
// with the fixed-behavior test above as documentation of what the fix
// actually changed.
func TestFoundryToolboxClient_Live_PreFixBehavior_WouldDropSkill_Regression9034(t *testing.T) {
	const toolboxName = "web-search-toolbox"

	svc := newFakeToolboxService(toolboxName, ToolboxVersionObject{
		Tools:  []map[string]any{{"type": "web_search", "name": "web_search"}},
		Skills: []map[string]any{},
	})
	client := newTestToolboxClient(
		"https://example.services.ai.azure.com/api/projects/agent-with-toolbox-dev",
		roundTripFunc(svc.handle),
	)
	ctx := context.Background()

	// addSkillFromDefault replicates the PRE-FIX behavior: always branch
	// from tb.DefaultVersion, regardless of what the latest version is.
	addSkillFromDefault := func(skillName string) string {
		tb, err := client.GetToolbox(ctx, toolboxName)
		require.NoError(t, err)

		current, err := client.GetToolboxVersion(ctx, toolboxName, tb.DefaultVersion)
		require.NoError(t, err)

		newSkills := append(append([]map[string]any{}, current.Skills...), map[string]any{
			"type": "skill_reference", "name": skillName,
		})
		created, err := client.CreateToolboxVersion(ctx, toolboxName, &CreateToolboxVersionRequest{
			Tools: current.Tools, Skills: newSkills,
		})
		require.NoError(t, err)
		return created.Version
	}

	v2 := addSkillFromDefault("greeting-test-2606")
	require.Equal(t, "2", v2)
	v3 := addSkillFromDefault("code-review-test-2606")
	require.Equal(t, "3", v3)

	shown, err := client.GetToolboxVersion(ctx, toolboxName, "3")
	require.NoError(t, err)
	require.Len(t, shown.Skills, 1,
		"pre-fix behavior: v3 only has code-review-test-2606 — greeting-test-2606 "+
			"was silently dropped because both adds branched from the stale default (v1)")
	require.Equal(t, "code-review-test-2606", shown.Skills[0]["name"])
}
