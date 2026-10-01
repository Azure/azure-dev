// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package eval_api

import (
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEvaluatorPointReadFailureIsNotAbsence(t *testing.T) {
	for _, version := range []string{"", "7"} {
		for _, status := range []int{http.StatusNotFound, http.StatusForbidden, http.StatusServiceUnavailable} {
			t.Run(fmt.Sprintf("version=%s/status=%d", version, status), func(t *testing.T) {
				var requests []string
				client, _ := clientAndServer(t, func(w http.ResponseWriter, r *http.Request) {
					requests = append(requests, r.URL.Path)
					w.Header().Set("Content-Type", "application/json")
					if strings.HasSuffix(r.URL.Path, "/versions") {
						_, err := w.Write([]byte(`{"value":[{"name":"e","version":"7"}]}`))
						assert.NoError(t, err)
						return
					}
					w.WriteHeader(status)
				})
				_, err := client.GetEvaluatorRaw(t.Context(), "e", version, "v1")
				require.Error(t, err)
				wrapped := fmt.Errorf("caller context: %w", err)
				assert.False(t, IsEvaluatorAbsent(wrapped), "a version read is not an evaluator listing")
				assert.Equal(t, status == http.StatusNotFound, IsNotFound(wrapped),
					"ordinary not-found handling must retain the HTTP status")
				if status == http.StatusForbidden {
					var authError *azdext.LocalError
					require.ErrorAs(t, wrapped, &authError, "retain the client's structured auth classification")
				} else {
					var response *azcore.ResponseError
					require.ErrorAs(t, wrapped, &response)
					assert.Equal(t, status, response.StatusCode)
				}
				if version == "" {
					assert.Equal(t, []string{"/evaluators/e/versions", "/evaluators/e/versions/7"}, requests)
				} else {
					assert.Equal(t, []string{"/evaluators/e/versions/7"}, requests)
				}
			})
		}
	}
}

func TestEvaluatorInitialListingStillEstablishesAbsence(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		body   string
	}{
		{"listing not found", http.StatusNotFound, ""},
		{"complete empty listing", http.StatusOK, `{"value":[]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests []string
			client, _ := clientAndServer(t, func(w http.ResponseWriter, r *http.Request) {
				requests = append(requests, r.URL.Path)
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				_, err := w.Write([]byte(tc.body))
				assert.NoError(t, err)
			})
			_, err := client.GetEvaluatorRaw(t.Context(), "e", "", "v1")
			require.Error(t, err)
			assert.True(t, IsEvaluatorAbsent(fmt.Errorf("wrapped: %w", err)))
			assert.Equal(t, []string{"/evaluators/e/versions"}, requests)
		})
	}
}
