package templates

import (
	"context"
	"fmt"
	"io"
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
)

// NewUrlTemplateSource creates a new template source from a URL.
func NewUrlTemplateSource(ctx context.Context, url string) (TemplateSource, error) {
	pipeline := runtime.NewPipeline("azd-templates", "version", runtime.PipelineOptions{}, nil)

	req, err := runtime.NewRequest(ctx, http.MethodGet, url)
	if err != nil {
		return nil, err
	}

	resp, err := pipeline.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed for template source '%s', %w", url, err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, runtime.NewResponseError(resp)
	}

	json, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed reading response body for template source '%s', %w", url, err)
	}

	return NewJsonTemplateSource(string(json))
}
