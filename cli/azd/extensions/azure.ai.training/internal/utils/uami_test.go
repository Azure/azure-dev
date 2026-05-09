// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package utils

import (
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/cognitiveservices/armcognitiveservices"
)

func TestProjectHasUAMI(t *testing.T) {
	uami := map[string]*armcognitiveservices.UserAssignedIdentity{
		"/subscriptions/x/resourceGroups/y/providers/Microsoft.ManagedIdentity/userAssignedIdentities/mi": {},
	}

	tests := []struct {
		name     string
		identity *armcognitiveservices.Identity
		want     bool
	}{
		{
			name:     "nil identity",
			identity: nil,
			want:     false,
		},
		{
			name:     "nil type",
			identity: &armcognitiveservices.Identity{Type: nil, UserAssignedIdentities: uami},
			want:     false,
		},
		{
			name: "SystemAssigned only",
			identity: &armcognitiveservices.Identity{
				Type: to.Ptr(armcognitiveservices.ResourceIdentityTypeSystemAssigned),
			},
			want: false,
		},
		{
			name: "UserAssigned type but empty map",
			identity: &armcognitiveservices.Identity{
				Type:                   to.Ptr(armcognitiveservices.ResourceIdentityTypeUserAssigned),
				UserAssignedIdentities: nil,
			},
			want: false,
		},
		{
			name: "UserAssigned type with populated map",
			identity: &armcognitiveservices.Identity{
				Type:                   to.Ptr(armcognitiveservices.ResourceIdentityTypeUserAssigned),
				UserAssignedIdentities: uami,
			},
			want: true,
		},
		{
			name: "SystemAssigned, UserAssigned with populated map",
			identity: &armcognitiveservices.Identity{
				Type:                   to.Ptr(armcognitiveservices.ResourceIdentityTypeSystemAssignedUserAssigned),
				UserAssignedIdentities: uami,
			},
			want: true,
		},
		{
			name: "case-insensitive 'userassigned' substring",
			identity: &armcognitiveservices.Identity{
				Type:                   to.Ptr(armcognitiveservices.ResourceIdentityType("userassigned")),
				UserAssignedIdentities: uami,
			},
			want: true,
		},
		{
			name: "None type",
			identity: &armcognitiveservices.Identity{
				Type:                   to.Ptr(armcognitiveservices.ResourceIdentityTypeNone),
				UserAssignedIdentities: uami,
			},
			want: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ProjectHasUAMI(tc.identity)
			if got != tc.want {
				t.Errorf("ProjectHasUAMI() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBoolEnv(t *testing.T) {
	if BoolEnv(true) != "true" {
		t.Errorf("BoolEnv(true) = %q, want %q", BoolEnv(true), "true")
	}
	if BoolEnv(false) != "false" {
		t.Errorf("BoolEnv(false) = %q, want %q", BoolEnv(false), "false")
	}
}

func TestNoUAMIMessage(t *testing.T) {
	msg := NoUAMIMessage("my-project")

	if !strings.Contains(msg, `"my-project"`) {
		t.Errorf("expected message to quote project name, got: %s", msg)
	}
	if !strings.Contains(msg, "User-Assigned Managed Identity") {
		t.Errorf("expected message to mention 'User-Assigned Managed Identity', got: %s", msg)
	}
	if !strings.Contains(msg, "azd ai training job submit") {
		t.Errorf("expected message to mention the failing command, got: %s", msg)
	}
}
