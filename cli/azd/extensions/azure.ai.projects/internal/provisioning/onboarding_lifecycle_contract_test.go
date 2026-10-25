// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package provisioning

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"azure.ai.projects/internal/onboarding"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func contractLifecycleResponse(request *http.Request, status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status, Header: http.Header{"Content-Type": {"application/json"}},
		Body: io.NopCloser(strings.NewReader(body)), Request: request,
	}
}

func lifecycleObject(t *testing.T, value any) map[string]any {
	t.Helper()
	object, ok := value.(map[string]any)
	require.True(t, ok, "expected an object, got %T", value)
	return object
}

func TestBYOSelectedOperationsFollowEffectiveOnDiskInputs(t *testing.T) {
	p := byoContractProvider(t)
	const principal = "00000000-0000-0000-0000-000000000044"
	p.onDiskSource = &templateSource{
		mode: templateModeBicep, armTemplate: p.armTemplate, sourcePath: "offline-main.bicep",
		parameters: map[string]any{
			"principalId":   map[string]any{"value": principal},
			"principalType": map[string]any{"value": "ServicePrincipal"},
			"deployments":   map[string]any{"value": []any{map[string]any{"name": "selected-model"}}},
		},
	}
	var checked bool
	p.byoPipeline = &onboarding.Pipeline{
		Resources: byoResourceContract(func(context.Context, onboarding.Plan) (onboarding.Facts, onboarding.Report, error) {
			return onboarding.Facts{KubeletObjectID: principal}, nil, nil
		}),
		Access: byoAccessContract(func(_ context.Context, plan onboarding.Plan, _ onboarding.Facts) (onboarding.Report, error) {
			checked = true
			var models, connections, developer bool
			for _, op := range plan.Operations {
				models = models || op.Action == "Microsoft.CognitiveServices/accounts/deployments/write"
				connections = connections || op.Action == "Microsoft.CognitiveServices/accounts/projects/connections/write"
				developer = developer || op.RoleID == "a97b65f3-24c7-4388-baec-2e87135dc908"
			}
			assert.False(t, models, "ordinary model deployment is not a BYO permission precheck")
			assert.False(t, connections, "ordinary ACR connection is not a BYO permission precheck")
			assert.False(t, developer, "developer grant remains in the separate predeploy lifecycle")
			return nil, nil
		}),
	}
	require.NoError(t, p.planBYO(t.Context(), func(string) {}, false))
	assert.True(t, checked)
	assert.Equal(t, []string{"selected-model"}, p.byoSpec.ModelNames,
		"removing model permission inference does not remove the actual deployment")
}

func TestBYOPlannedGroupTagsProtectOwnershipAndExplicitChoices(t *testing.T) {
	for _, test := range []struct {
		name     string
		observed map[string]string
		explicit map[string]string
		want     map[string]string
		conflict bool
	}{
		{
			name: "explicit case-insensitive customer change",
			observed: map[string]string{
				"customer-billing": "old", "untouched": "preserve", "azd-byoaks-owner": "owned",
			},
			explicit: map[string]string{"Customer-Billing": "new"},
			want: map[string]string{
				"Customer-Billing": "new", "untouched": "preserve", "azd-byoaks-owner": "owned",
			},
		},
		{
			name:     "same owner alias preserves original key",
			observed: map[string]string{"azd-byoaks-owner": "owned"},
			explicit: map[string]string{"AZD-BYOAKS-OWNER": "owned"},
			want:     map[string]string{"azd-byoaks-owner": "owned"},
		},
		{
			name: "changed ownership", observed: map[string]string{"azd-byoaks-owner": "owned"},
			explicit: map[string]string{"azd-byoaks-owner": "other"}, conflict: true,
		},
		{
			name: "changed case-insensitive ownership", observed: map[string]string{"azd-byoaks-owner": "owned"},
			explicit: map[string]string{"AZD-BYOAKS-OWNER": "other"}, conflict: true,
		},
		{
			name: "empty ownership", observed: map[string]string{"azd-byoaks-owner": "owned"},
			explicit: map[string]string{"azd-byoaks-owner": ""}, conflict: true,
		},
		{
			name: "cannot adopt an unowned group", observed: map[string]string{"customer": "preserve"},
			explicit: map[string]string{"azd-byoaks-owner": "owned"}, conflict: true,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			p := byoContractProvider(t)
			p.synthResult.Parameters["tags"] = test.observed
			parameters := map[string]any{"tags": map[string]any{"value": test.explicit}}
			err := p.bindPlannedGroupTags(parameters)
			if test.conflict {
				require.ErrorContains(t, err, "private azd-byoaks-owner")
				assert.Equal(t, test.explicit, parameters["tags"].(map[string]any)["value"])
				return
			}
			require.NoError(t, err)
			assert.Equal(t, test.want, parameters["tags"].(map[string]any)["value"])
		})
	}
}
