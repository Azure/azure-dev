package input

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"

	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
)

// externalPromptClient is a client for the external prompt service, as described in [../../docs/external-prompt.md].
type externalPromptClient struct {
	endpoint string
	key      string
	pipeline httputil.HttpClient
}

type promptOptions struct {
	Type    string               `json:"type"`
	Options promptOptionsOptions `json:"options"`
}

type promptChoice struct {
	Value  string  `json:"value"`
	Detail *string `json:"detail,omitempty"`
}

type promptOptionsOptions struct {
	Message      string          `json:"message"`
	Help         string          `json:"help"`
	Choices      *[]promptChoice `json:"choices,omitempty"`
	DefaultValue *any            `json:"defaultValue,omitempty"`
}

type promptResponse struct {
	// Status is one of "success", "error", or "cancelled".
	Status string `json:"status"`

	// These fields are set when status is "success"

	// Value is either a string or an array of strings, encoded in JSON.
	Value *json.RawMessage `json:"value,omitempty"`

	// These fields are set when the status is "error"

	// Message is a human-readable error message.
	Message *string `json:"message,omitempty"`
}

func newExternalPromptClient(endpoint string, key string, pipeline httputil.HttpClient) *externalPromptClient {
	return &externalPromptClient{
		endpoint: endpoint,
		key:      key,
		pipeline: pipeline,
	}
}

var (
	// promptCancelledErr is the error that is returned by Prompt when the prompt is cancelled by the user.
	promptCancelledErr = errors.New("cancelled")
)

func (c *externalPromptClient) Prompt(ctx context.Context, options promptOptions) (json.RawMessage, error) {
	body, err := json.Marshal(options)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		fmt.Sprintf("%s/prompt?api-version=2024-02-14-preview", c.endpoint),
		bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.key))

	res, err := c.pipeline.Do(req)
	if err != nil {
		return nil, fmt.Errorf("making request: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", res.StatusCode)
	}

	var resp promptResponse

	if err := json.NewDecoder(res.Body).Decode(&resp); err != nil {
		return nil, fmt.Errorf("unmarshalling response: %w", err)
	}

	switch resp.Status {
	case "success":
		return *resp.Value, nil
	case "error":
		return nil, fmt.Errorf("prompt error: %s", *resp.Message)
	case "cancelled":
		return nil, promptCancelledErr
	default:
		return nil, fmt.Errorf("unexpected result: %s", resp.Status)
	}
}
