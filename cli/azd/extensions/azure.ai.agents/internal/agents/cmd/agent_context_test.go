// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import "testing"

func TestBuildAgentEndpoint_Cases(t *testing.T) {
	t.Parallel()
	tests := []struct{ account, project, want string }{
		{"myaccount", "myproject", "https://myaccount.services.ai.azure.com/api/projects/myproject"},
		{"a", "b", "https://a.services.ai.azure.com/api/projects/b"},
	}
	for _, tt := range tests {
		t.Run(tt.account+"/"+tt.project, func(t *testing.T) {
			t.Parallel()
			got := buildAgentEndpoint(tt.account, tt.project)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}
