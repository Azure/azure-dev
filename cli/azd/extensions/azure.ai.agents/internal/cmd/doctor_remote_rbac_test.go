// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"strings"
	"testing"
)

func TestParseDoctorProjectID(t *testing.T) {
	const valid = "/subscriptions/00000000-0000-0000-0000-000000000001" +
		"/resourceGroups/rg-foundry" +
		"/providers/Microsoft.CognitiveServices/accounts/myacct" +
		"/projects/myproj"

	t.Run("valid", func(t *testing.T) {
		info, err := parseDoctorProjectID(valid)
		if err != nil {
			t.Fatalf("err = %v, want nil", err)
		}
		if info.subscriptionID != "00000000-0000-0000-0000-000000000001" {
			t.Errorf("subscriptionID = %q", info.subscriptionID)
		}
		if info.resourceGroup != "rg-foundry" {
			t.Errorf("resourceGroup = %q", info.resourceGroup)
		}
		if info.accountName != "myacct" {
			t.Errorf("accountName = %q", info.accountName)
		}
		if info.projectName != "myproj" {
			t.Errorf("projectName = %q", info.projectName)
		}
		if info.projectScope != valid {
			t.Errorf("projectScope = %q", info.projectScope)
		}
		if !strings.HasSuffix(info.accountScope, "/accounts/myacct") {
			t.Errorf("accountScope = %q", info.accountScope)
		}
		if strings.Contains(info.accountScope, "/projects/") {
			t.Errorf("accountScope should not include /projects/: %q", info.accountScope)
		}
	})

	cases := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"too_few_segments", "/subscriptions/abc/resourceGroups/rg"},
		{"missing_project", "/subscriptions/abc/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/x"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := parseDoctorProjectID(tc.in); err == nil {
				t.Fatalf("parseDoctorProjectID(%q) returned nil error", tc.in)
			}
		})
	}
}

func TestMatchingRoleNames(t *testing.T) {
	t.Run("preserves_want_order", func(t *testing.T) {
		want := []string{doctorRoleOwner, doctorRoleAzureAIDeveloper, doctorRoleContributor}
		present := map[string]bool{
			doctorRoleAzureAIDeveloper: true,
			doctorRoleOwner:            true,
			doctorRoleContributor:      true,
		}
		got := matchingRoleNames(want, present)
		expected := []string{"Owner", "Azure AI Developer", "Contributor"}
		if !slicesEqual(got, expected) {
			t.Fatalf("got = %v, want %v", got, expected)
		}
	})

	t.Run("filters_unknown", func(t *testing.T) {
		got := matchingRoleNames(
			[]string{doctorRoleOwner},
			map[string]bool{"some-other-guid": true},
		)
		if len(got) != 0 {
			t.Fatalf("got = %v, want []", got)
		}
	})
}

func TestDedupRoles(t *testing.T) {
	got := dedupRoles([]string{"Owner", "Contributor", "Owner", "Azure AI Developer", "Contributor"})
	want := []string{"Owner", "Contributor", "Azure AI Developer"}
	if !slicesEqual(got, want) {
		t.Fatalf("got = %v, want %v", got, want)
	}
}

func TestRoleAssignCommand(t *testing.T) {
	got := roleAssignCommand(
		"11111111-1111-1111-1111-111111111111",
		"Azure AI Developer",
		"/subscriptions/sub/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/a/projects/p",
	)
	if !strings.Contains(got, "az role assignment create") {
		t.Errorf("missing command verb: %q", got)
	}
	if !strings.Contains(got, "--assignee 11111111-1111-1111-1111-111111111111") {
		t.Errorf("missing --assignee: %q", got)
	}
	if !strings.Contains(got, `--role "Azure AI Developer"`) {
		t.Errorf("missing quoted --role: %q", got)
	}
	if !strings.Contains(got, "--scope /subscriptions/sub/") {
		t.Errorf("missing --scope: %q", got)
	}
}

func TestClassifyRBAC(t *testing.T) {
	info := &doctorProjectInfo{
		subscriptionID: "sub",
		resourceGroup:  "rg",
		accountName:    "acct",
		projectName:    "proj",
		projectScope: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.CognitiveServices/" +
			"accounts/acct/projects/proj",
		accountScope: "/subscriptions/sub/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/acct",
	}
	const principal = "22222222-2222-2222-2222-222222222222"
	const label = "Alice (alice@contoso.com)"
	sets := defaultRBACRoleSets()

	t.Run("pass_with_developer_and_cognitive_user", func(t *testing.T) {
		got := classifyRBAC(
			sets,
			map[string]bool{
				doctorRoleAzureAIDeveloper:     true,
				doctorRoleCognitiveServicesSvc: true,
			},
			principal, label, info,
		)
		if got.Status != doctorOK {
			t.Fatalf("status = %v, want OK; detail=%q", got.Status, got.Detail)
		}
		if !strings.Contains(got.Detail, "Azure AI Developer") ||
			!strings.Contains(got.Detail, "Cognitive Services User") {
			t.Errorf("detail missing roles: %q", got.Detail)
		}
		if got.Fix != "" {
			t.Errorf("Pass result should not have Fix, got %q", got.Fix)
		}
	})

	t.Run("pass_with_owner_only", func(t *testing.T) {
		got := classifyRBAC(
			sets,
			map[string]bool{doctorRoleOwner: true},
			principal, label, info,
		)
		if got.Status != doctorOK {
			t.Fatalf("status = %v, want OK", got.Status)
		}
		if !strings.Contains(got.Detail, "Owner") {
			t.Errorf("detail missing Owner: %q", got.Detail)
		}
		// Owner satisfies both deploy and model; dedup should not show
		// it twice.
		if strings.Count(got.Detail, "Owner") != 1 {
			t.Errorf("Owner appears more than once in detail: %q", got.Detail)
		}
	})

	t.Run("warn_invoke_only_with_model", func(t *testing.T) {
		got := classifyRBAC(
			sets,
			map[string]bool{
				doctorRoleAzureAIUser:          true,
				doctorRoleCognitiveServicesSvc: true,
			},
			principal, label, info,
		)
		if got.Status != doctorWarn {
			t.Fatalf("status = %v, want Warn; detail=%q", got.Status, got.Detail)
		}
		if !strings.Contains(got.Detail, "can invoke") {
			t.Errorf("warn detail missing 'can invoke': %q", got.Detail)
		}
		if !strings.Contains(got.Fix, "Azure AI Developer") {
			t.Errorf("warn fix missing AI Developer role: %q", got.Fix)
		}
		if !strings.Contains(got.Fix, principal) {
			t.Errorf("warn fix missing principal: %q", got.Fix)
		}
		if !strings.Contains(got.Fix, info.projectScope) {
			t.Errorf("warn fix missing scope: %q", got.Fix)
		}
	})

	t.Run("fail_missing_everything", func(t *testing.T) {
		got := classifyRBAC(sets, map[string]bool{}, principal, label, info)
		if got.Status != doctorFail {
			t.Fatalf("status = %v, want Fail", got.Status)
		}
		if !strings.Contains(got.Detail, "Azure AI Developer") {
			t.Errorf("fail detail missing deploy role hint: %q", got.Detail)
		}
		if !strings.Contains(got.Detail, "Cognitive Services User") {
			t.Errorf("fail detail missing model role hint: %q", got.Detail)
		}
		if !strings.Contains(got.Fix, "az role assignment create") {
			t.Errorf("fail fix missing CLI verb: %q", got.Fix)
		}
	})

	t.Run("fail_missing_model_only", func(t *testing.T) {
		got := classifyRBAC(
			sets,
			map[string]bool{doctorRoleAzureAIDeveloper: true},
			principal, label, info,
		)
		if got.Status != doctorFail {
			t.Fatalf("status = %v, want Fail", got.Status)
		}
		if strings.Contains(got.Detail, "Azure AI Developer (or stronger)") {
			t.Errorf("fail detail should not list deploy role as missing: %q", got.Detail)
		}
		if !strings.Contains(got.Detail, "Cognitive Services User") {
			t.Errorf("fail detail should mention missing model role: %q", got.Detail)
		}
	})
}

func TestCheckUserRBAC_SkipsWhenAuthDidNotPass(t *testing.T) {
	a := &doctorAction{}
	res := a.checkUserRBAC(t.Context(), remotePreconditions{}, doctorFail)
	if res.Status != doctorSkip {
		t.Fatalf("status = %v, want skip", res.Status)
	}
	if !strings.Contains(res.Detail, "authentication") {
		t.Errorf("detail should explain auth dependency: %q", res.Detail)
	}
	if res.ID != "remote.rbac" {
		t.Errorf("ID = %q, want remote.rbac", res.ID)
	}
}

func testProjectInfo() *doctorProjectInfo {
	return &doctorProjectInfo{
		subscriptionID: "00000000-0000-0000-0000-000000000001",
		resourceGroup:  "rg-foundry",
		accountName:    "acct",
		projectName:    "proj",
		projectScope: "/subscriptions/00000000-0000-0000-0000-000000000001" +
			"/resourceGroups/rg-foundry/providers/Microsoft.CognitiveServices/" +
			"accounts/acct/projects/proj",
		accountScope: "/subscriptions/00000000-0000-0000-0000-000000000001" +
			"/resourceGroups/rg-foundry/providers/Microsoft.CognitiveServices/accounts/acct",
	}
}

func TestRgScope(t *testing.T) {
	got := rgScope(testProjectInfo())
	want := "/subscriptions/00000000-0000-0000-0000-000000000001/resourceGroups/rg-foundry"
	if got != want {
		t.Fatalf("rgScope = %q, want %q", got, want)
	}
}

func TestBucketScope(t *testing.T) {
	info := testProjectInfo()
	cases := []struct {
		name  string
		scope string
		want  scopeBucket
	}{
		{"project", info.projectScope, scopeBucketProject},
		{"account", info.accountScope, scopeBucketAccount},
		{"resource_group", rgScope(info), scopeBucketResourceGroup},
		{"subscription", "/subscriptions/00000000-0000-0000-0000-000000000001", scopeBucketOther},
		{"random_resource", "/subscriptions/.../resourceGroups/other/providers/X/Y", scopeBucketOther},
		{"case_insensitive_project", strings.ToUpper(info.projectScope), scopeBucketProject},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := bucketScope(tc.scope, info); got != tc.want {
				t.Fatalf("bucketScope(%q) = %v, want %v", tc.scope, got, tc.want)
			}
		})
	}
}

func TestSortedKeys(t *testing.T) {
	got := sortedKeys(map[string]bool{"Owner": true, "Cognitive Services User": true, "Reader": true})
	want := []string{"Cognitive Services User", "Owner", "Reader"}
	if !slicesEqual(got, want) {
		t.Fatalf("got = %v, want %v", got, want)
	}
	if sortedKeys(nil) != nil {
		t.Errorf("sortedKeys(nil) should return nil")
	}
	if sortedKeys(map[string]bool{}) != nil {
		t.Errorf("sortedKeys({}) should return nil")
	}
}

func TestRoleLabelForID(t *testing.T) {
	if got := roleLabelForID(doctorRoleOwner); got != "Owner" {
		t.Errorf("Owner GUID -> %q, want Owner", got)
	}
	if got := roleLabelForID("not-a-known-guid"); got != "not-a-known-guid" {
		t.Errorf("unknown GUID should pass through, got %q", got)
	}
}

func TestRenderScopeBucket(t *testing.T) {
	if got := renderScopeBucket(nil); got != "  - (none)" {
		t.Errorf("empty bucket = %q, want '  - (none)'", got)
	}
	got := renderScopeBucket([]string{"Owner", "Reader"})
	want := "  - Owner\n  - Reader"
	if got != want {
		t.Errorf("two-role bucket = %q, want %q", got, want)
	}
}

func TestRenderAgentRoleSummary(t *testing.T) {
	got := renderAgentRoleSummary(
		"research-bot",
		"33333333-3333-3333-3333-333333333333",
		agentRoleSummary{
			project:       []string{"Cognitive Services User"},
			account:       nil,
			resourceGroup: []string{"Storage Blob Data Reader"},
		},
	)
	if !strings.Contains(got, "agent: research-bot") {
		t.Errorf("missing agent name: %q", got)
	}
	if !strings.Contains(got, "principal: 33333333-3333-3333-3333-333333333333") {
		t.Errorf("missing principal: %q", got)
	}
	if !strings.Contains(got, "project scope:\n  - Cognitive Services User") {
		t.Errorf("missing project scope list: %q", got)
	}
	if !strings.Contains(got, "account scope:\n  - (none)") {
		t.Errorf("missing empty account placeholder: %q", got)
	}
	if !strings.Contains(got, "resource-group scope:\n  - Storage Blob Data Reader") {
		t.Errorf("missing resource-group list: %q", got)
	}
}

func TestClassifyAgentRoleSummary(t *testing.T) {
	cases := []struct {
		name string
		in   agentRoleSummary
		want doctorStatus
	}{
		{"fail_all_empty", agentRoleSummary{}, doctorFail},
		{
			"info_project_and_account",
			agentRoleSummary{
				project: []string{"Cognitive Services User"},
				account: []string{"Reader"},
			},
			doctorInfo,
		},
		{
			"info_project_and_rg",
			agentRoleSummary{
				project:       []string{"Cognitive Services User"},
				resourceGroup: []string{"Storage Blob Data Reader"},
			},
			doctorInfo,
		},
		{
			"warn_project_only",
			agentRoleSummary{
				project: []string{"Cognitive Services User"},
			},
			doctorWarn,
		},
		{
			"warn_account_only",
			agentRoleSummary{
				account: []string{"Reader"},
			},
			doctorWarn,
		},
		{
			"warn_other_only",
			agentRoleSummary{
				other: []string{"Reader"},
			},
			doctorWarn,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyAgentRoleSummary(tc.in); got != tc.want {
				t.Fatalf("classifyAgentRoleSummary = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCheckAgentIdentityRBAC_SkipsWhenAuthDidNotPass(t *testing.T) {
	a := &doctorAction{}
	results := a.checkAgentIdentityRBAC(t.Context(), remotePreconditions{}, doctorFail, doctorOK)
	if len(results) != 1 {
		t.Fatalf("len = %d, want 1", len(results))
	}
	if results[0].Status != doctorSkip {
		t.Fatalf("status = %v, want skip", results[0].Status)
	}
	if results[0].ID != "remote.agent-rbac" {
		t.Errorf("ID = %q, want remote.agent-rbac", results[0].ID)
	}
}

func TestCheckAgentIdentityRBAC_SkipsWhenReachabilityDidNotPass(t *testing.T) {
	a := &doctorAction{}
	results := a.checkAgentIdentityRBAC(t.Context(), remotePreconditions{}, doctorOK, doctorFail)
	if len(results) != 1 || results[0].Status != doctorSkip {
		t.Fatalf("expected single skip row, got %+v", results)
	}
	if !strings.Contains(results[0].Detail, "reachability") {
		t.Errorf("detail = %q, want mention of reachability", results[0].Detail)
	}
}

func TestCheckAgentIdentityRBAC_SkipsWhenEndpointMissing(t *testing.T) {
	a := &doctorAction{}
	pre := remotePreconditions{endpointSet: false}
	results := a.checkAgentIdentityRBAC(t.Context(), pre, doctorOK, doctorOK)
	if len(results) != 1 || results[0].Status != doctorSkip {
		t.Fatalf("expected single skip row, got %+v", results)
	}
	if !strings.Contains(results[0].Detail, "AZURE_AI_PROJECT_ENDPOINT") {
		t.Errorf("detail = %q, want mention of AZURE_AI_PROJECT_ENDPOINT", results[0].Detail)
	}
}

// slicesEqual is a tiny helper local to this test file — avoids pulling
// reflect or slices.Equal into the doctor test surface for what is a
// 3-line check.
func slicesEqual[T comparable](a, b []T) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
