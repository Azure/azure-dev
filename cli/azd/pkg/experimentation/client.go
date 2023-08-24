package experimentation

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
)

var clientVersion string = "0.0.1"

type tasClient struct {
	tasEndpoint string
	pipeline    runtime.Pipeline
}

type variantAssignmentRequest struct {
	Parameters       map[string]string
	AssignmentScopes []string
	RequiredVariants []string
	BlockedVariants  []string
}

type treatmentAssignmentResponse struct {
	Features []string          `json:"Features"`
	Flights  map[string]string `json:"Flights"`
	Configs  []struct {
		ID         string                 `json:"Id"`
		Parameters map[string]interface{} `json:"Parameters"`
	} `json:"Configs"`
	ParameterGroups   []string `json:"ParameterGroups"`
	FlightingVersion  int64    `json:"FlightingVersion"`
	ImpressionID      string   `json:"ImpressionId"`
	AssignmentContext string   `json:"AssignmentContext"`
}

// newTasClient creates a new instance of the treatment assignments client.
func newTasClient(tasEndpoint string, options *azcore.ClientOptions) (*tasClient, error) {
	if tasEndpoint == "" {
		return nil, fmt.Errorf("tasEndpoint must be set")
	}

	if options == nil {
		options = &azcore.ClientOptions{}
	}

	pipeline := runtime.NewPipeline("variantassignment", clientVersion, runtime.PipelineOptions{}, options)

	return &tasClient{
		tasEndpoint: tasEndpoint,
		pipeline:    pipeline,
	}, nil
}

// GetVariantAssignments gets the variant assignments for the given request.
func (c *tasClient) GetVariantAssignments(
	ctx context.Context,
	request *variantAssignmentRequest,
) (*treatmentAssignmentResponse, error) {
	req, err := runtime.NewRequest(ctx, http.MethodGet, c.tasEndpoint)
	if err != nil {
		return nil, err
	}

	// Add sdk headers
	req.Raw().Header.Add("Accept", "application/json")
	req.Raw().Header.Add("X-ExP-SDK-Version", clientVersion)

	setHeaderValues(req.Raw(), "X-ExP-Parameters", escapeParameterStrings(request.Parameters))
	setHeaderValues(req.Raw(), "X-ExP-AssignmentScopes", escapeDataStrings(request.AssignmentScopes))
	setHeaderValues(req.Raw(), "X-ExP-RequiredVariants", escapeDataStrings(request.RequiredVariants))
	setHeaderValues(req.Raw(), "X-ExP-BlockedVariants", escapeDataStrings(request.BlockedVariants))

	resp, err := c.pipeline.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send HTTP request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected response status code: %d", resp.StatusCode)
	}

	var response treatmentAssignmentResponse
	err = json.NewDecoder(resp.Body).Decode(&response)
	if err != nil {
		return nil, err
	}

	return &response, nil
}

// setHeaderValues sets the values of the given header if the values are not empty.
func setHeaderValues(request *http.Request, headerName string, values []string) {
	if len(values) > 0 {
		request.Header[headerName] = values
	}
}

// escapeParameterStrings returns a slice of query escaped parameter strings.
func escapeParameterStrings(values map[string]string) []string {
	if values == nil {
		return nil
	}
	escapedValues := make([]string, 0, len(values))
	for key, value := range values {
		if key == "" || value == "" {
			continue
		}
		encodedParameter := url.QueryEscape(key) + "=" + url.QueryEscape(value)
		escapedValues = append(escapedValues, encodedParameter)
	}
	return escapedValues
}

// escapeDataStrings returns a copy of values, after [url.QueryEscape]ing each element.
func escapeDataStrings(values []string) []string {
	if values == nil {
		return nil
	}
	escapedValues := make([]string, 0, len(values))
	for _, value := range values {
		if value != "" {
			escapedValues = append(escapedValues, url.QueryEscape(value))
		}
	}
	return escapedValues
}
