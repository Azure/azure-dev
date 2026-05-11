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
