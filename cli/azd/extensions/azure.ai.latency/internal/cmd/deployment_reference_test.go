// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"strings"
	"testing"
)

func TestParseDeploymentID(t *testing.T) {
	const deploymentID = "/subscriptions/sub/resourceGroups/rg/providers/" +
		"Microsoft.CognitiveServices/accounts/account/deployments/deployment"
	reference, err := parseDeploymentID(deploymentID)
	if err != nil {
		t.Fatal(err)
	}
	if reference.Subscription != "sub" ||
		reference.ResourceGroup != "rg" ||
		reference.AccountName != "account" ||
		reference.DeploymentName != "deployment" {
		t.Fatalf("unexpected reference: %+v", reference)
	}
}

func TestParseDeploymentIDRejectsAccountResource(t *testing.T) {
	_, err := parseDeploymentID(
		"/subscriptions/sub/resourceGroups/rg/providers/Microsoft.CognitiveServices/accounts/account",
	)
	if err == nil {
		t.Fatal("expected account resource ID to fail")
	}
}

func TestResolveDeploymentReferenceNoPromptNamesMissingFlags(t *testing.T) {
	_, err := resolveDeploymentReference(t.Context(), deploymentFlags{}, true, nil)
	if err == nil {
		t.Fatal("expected missing deployment reference to fail")
	}
	for _, expected := range []string{"--subscription", "--deployment-name"} {
		if !strings.Contains(err.Error(), expected) {
			t.Fatalf("error %q does not contain %q", err, expected)
		}
	}
}

func TestResolveDeploymentReferenceRejectsConflictingForms(t *testing.T) {
	_, err := resolveDeploymentReference(t.Context(), deploymentFlags{
		deploymentID: "/subscriptions/sub/resourceGroups/rg/providers/" +
			"Microsoft.CognitiveServices/accounts/account/deployments/deployment",
		subscription: "sub",
	}, true, nil)
	if err == nil {
		t.Fatal("expected conflicting deployment forms to fail")
	}
}
