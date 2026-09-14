// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package containerref

import (
	"strings"
	"testing"
)

func TestImageReferences(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		image              string
		wantValid          bool
		wantFullyQualified bool
	}{
		{
			name:               "registry and tag",
			image:              "registry.example.com/team/agent:v1",
			wantValid:          true,
			wantFullyQualified: true,
		},
		{
			name:               "registry port",
			image:              "registry:5000/team/agent:v1",
			wantValid:          true,
			wantFullyQualified: true,
		},
		{
			name:               "localhost and port",
			image:              "localhost:5000/agent:latest",
			wantValid:          true,
			wantFullyQualified: true,
		},
		{
			name:               "IPv6 registry",
			image:              "[2001:db8::1]:5000/team/agent:v1",
			wantValid:          true,
			wantFullyQualified: true,
		},
		{
			name:               "digest",
			image:              "registry.example.com/agent@sha256:" + strings.Repeat("a", 64),
			wantValid:          true,
			wantFullyQualified: true,
		},
		{
			name:               "tag and digest",
			image:              "registry.example.com/agent:v1@sha256:" + strings.Repeat("a", 64),
			wantValid:          true,
			wantFullyQualified: true,
		},
		{name: "unqualified path", image: "team/agent:v1", wantValid: true},
		{name: "unqualified", image: "agent:v1", wantValid: true},
		{name: "URL scheme", image: "https://registry.example.com/agent:v1"},
		{name: "uppercase repository", image: "registry.example.com/Team/agent:v1"},
		{name: "empty"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := IsValid(test.image); got != test.wantValid {
				t.Errorf("IsValid(%q) = %t, want %t", test.image, got, test.wantValid)
			}
			if got := IsFullyQualified(test.image); got != test.wantFullyQualified {
				t.Errorf(
					"IsFullyQualified(%q) = %t, want %t",
					test.image,
					got,
					test.wantFullyQualified,
				)
			}
		})
	}
}
