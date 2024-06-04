package templates

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/runtime"
	"github.com/azure/azure-dev/cli/azd/pkg/github"
	"github.com/azure/azure-dev/cli/azd/pkg/httputil"
)

type awesomeAzdTemplate struct {
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Source      string   `json:"source"`
	Tags        []string `json:"tags"`
}

// NewAwesomeAzdTemplateSource creates a new template source from the awesome-azd templates json file.
func NewAwesomeAzdTemplateSource(
	ctx context.Context,
	name string,
	url string,
	httpClient httputil.HttpClient,
) (Source, error) {
	pipeline := runtime.NewPipeline("azd-templates", "1.0.0", runtime.PipelineOptions{}, &policy.ClientOptions{
		Transport: httpClient,
	})

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

	templatesJson, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed reading response body for template source '%s', %w", url, err)
	}

	var rawAwesomeAzdTemplates []*awesomeAzdTemplate
	if err := json.Unmarshal(templatesJson, &rawAwesomeAzdTemplates); err != nil {
		return nil, fmt.Errorf("failed to unmarshal templates json: %w", err)
	}

	awesomeAzdTemplates := []*Template{}
	for _, template := range rawAwesomeAzdTemplates {
		if template.Title == "" || template.Source == "" {
			log.Println("skipping template. missing required attributes")
			continue
		}

		repoPath, err := github.GetSlugForRemote(template.Source)
		if err != nil {
			repoPath = template.Source
		}

		awesomeAzdTemplates = append(awesomeAzdTemplates, &Template{
			Name:           template.Title,
			Description:    template.Description,
			RepositoryPath: repoPath,
			Tags:           template.Tags,
		})
	}

	return NewTemplateSource(name, awesomeAzdTemplates)
}
