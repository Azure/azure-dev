package experimentation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
	parameterValues := map[string]string{
		"key1": "value1",
		"key2": "value2",
	}
	escapedValues := escapeParameterStrings(parameterValues)
	if len(escapedValues) != 2 {
		t.Errorf("Expected 2 escaped values, got %d", len(escapedValues))
	}
	// Try with null value
	escapedValues = escapeParameterStrings(nil)
	if escapedValues != nil {
		t.Errorf("Expected 0 escaped values, got %d", len(escapedValues))
	}
	fmt.Println("get escaped parameter strings test passed")
}

func TestGetEscapedDataStrings(t *testing.T) {
	inputValues := []string{"value1", "value2"}
	escapedValues := escapeDataStrings(inputValues)
	if len(escapedValues) != 2 {
		t.Errorf("Expected 2 escaped values, got %d", len(escapedValues))
	}
	// Try with null value
	escapedValues = escapeDataStrings(nil)
	if escapedValues != nil {
		t.Errorf("Expected 0 escaped values, got %d", len(escapedValues))
	}
	fmt.Println("get escaped data strings test passed")
}
