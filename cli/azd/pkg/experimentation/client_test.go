package experimentation

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"testing"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/azure/azure-dev/cli/azd/test/mocks/mockhttp"
	"github.com/stretchr/testify/require"
)

func TestGetVariantAssignments(t *testing.T) {
	endpoint := "https://test-exp-s2s.msedge.net/ab"

	mockHttp := mockhttp.NewMockHttpUtil()
	mockHttp.When(func(request *http.Request) bool {
		return request.Method == http.MethodGet && request.URL.String() == endpoint
	}).RespondFn(
		func(request *http.Request) (*http.Response, error) {
			// nolint: staticcheck
			require.ElementsMatch(t, request.Header["X-ExP-Parameters"], []string{"key1=value1", "key2=value2"})
			// nolint: staticcheck
			require.Equal(t, request.Header["X-ExP-RequiredVariants"], []string{"variant1", "variant2"})
			// nolint: staticcheck
			require.Equal(t, request.Header["X-ExP-BlockedVariants"], []string{"variant3", "variant4"})
			// nolint: staticcheck
			require.Equal(t, request.Header["X-ExP-AssignmentScopes"], []string(nil))

			res := treatmentAssignmentResponse{
				FlightingVersion:  1,
				AssignmentContext: "context:393182",
			}

			jsonBytes, _ := json.Marshal(res)

			return &http.Response{
				Request:    request,
				StatusCode: http.StatusOK,
				Header:     http.Header{},
				Body:       io.NopCloser(bytes.NewBuffer(jsonBytes)),
			}, nil
		},
	)
	client, err := newTasClient("https://test-exp-s2s.msedge.net/ab", &azcore.ClientOptions{
		Transport: mockHttp,
	})
	require.NoError(t, err)

	request := variantAssignmentRequest{
		Parameters: map[string]string{
			"key1": "value1",
			"key2": "value2",
		},
		AssignmentScopes: []string{""},
		RequiredVariants: []string{"variant1", "variant2"},
		BlockedVariants:  []string{"variant3", "variant4"},
	}
	resp, err := client.GetVariantAssignments(context.Background(), &request)
	require.NoError(t, err)
	require.Equal(t, "context:393182", resp.AssignmentContext)
	require.Equal(t, int64(1), resp.FlightingVersion)
}

func TestGetEscapedParameterStrings(t *testing.T) {
	tests := []struct {
		name            string
		parameterValues map[string]string
		expectedEscaped []string
	}{
		{
			name: "valid parameters",
			parameterValues: map[string]string{
				"key1": "value1",
				"key2": "value2",
			},
			expectedEscaped: []string{"key1=value1", "key2=value2"},
		},
		{
			name: "escaped parameters",
			parameterValues: map[string]string{
				"ke&y1": "val&ue1",
				"ke&y2": "val&ue2",
			},
			expectedEscaped: []string{"ke%26y1=val%26ue1", "ke%26y2=val%26ue2"},
		},
		{
			name:            "nil parameters",
			parameterValues: nil,
			expectedEscaped: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			escapedValues := escapeParameterStrings(tt.parameterValues)
			require.ElementsMatch(t, tt.expectedEscaped, escapedValues)
		})
	}
}

func TestGetEscapedDataStrings(t *testing.T) {
	tests := []struct {
		name            string
		inputValues     []string
		expectedEscaped []string
	}{
		{
			name: "valid data",
			inputValues: []string{
				"value1",
				"value2",
			},
			expectedEscaped: []string{
				"value1",
				"value2",
			},
		},
		{
			name: "escaped data",
			inputValues: []string{
				"val&ue1",
				"val&ue2",
			},
			expectedEscaped: []string{
				"val%26ue1",
				"val%26ue2",
			},
		},
		{
			name:            "nil data",
			inputValues:     nil,
			expectedEscaped: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			escapedValues := escapeDataStrings(tt.inputValues)
			require.ElementsMatch(t, tt.expectedEscaped, escapedValues)
		})
	}
}
