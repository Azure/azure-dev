package llm

import (
	"fmt"
	"maps"
	"os"

	"github.com/azure/azure-dev/cli/azd/pkg/output/ux"
	"github.com/tmc/langchaingo/llms/openai"
)

const (
	modelEnvVar   = "AZD_AZURE_OPENAI_MODEL"
	versionEnvVar = "AZD_AZURE_OPENAI_VERSION"
	urlEnvVar     = "AZD_AZURE_OPENAI_URL"
	keyEnvVar     = "OPENAI_API_KEY"
)

type requiredEnvVar struct {
	name      string
	value     string
	isDefined bool
}

var requiredEnvVars = map[string]requiredEnvVar{
	modelEnvVar:   {name: modelEnvVar},
	versionEnvVar: {name: versionEnvVar},
	urlEnvVar:     {name: urlEnvVar},
	keyEnvVar:     {name: keyEnvVar},
}

func loadAzureOpenAi() (InfoResponse, error) {

	envVars := maps.Clone(requiredEnvVars)
	hasMissing := false
	for name, envVar := range envVars {
		if value, isDefined := os.LookupEnv(envVar.name); isDefined {
			envVar.value = value
			envVar.isDefined = true
		} else {
			hasMissing = true
		}
		envVars[name] = envVar
	}
	if hasMissing {
		missingEnvVars := []string{}
		for _, ev := range envVars {
			if !ev.isDefined {
				missingEnvVars = append(missingEnvVars, ev.name)
			}
		}
		return InfoResponse{}, fmt.Errorf(
			"missing required environment variable(s): %s", ux.ListAsText(missingEnvVars))
	}

	_, err := openai.New(
		openai.WithModel(envVars[modelEnvVar].value),
		openai.WithAPIType(openai.APITypeAzure),
		openai.WithAPIVersion(envVars[versionEnvVar].value),
		openai.WithBaseURL(envVars[urlEnvVar].value),
	)
	if err != nil {
		return InfoResponse{}, fmt.Errorf("failed to create LLM: %w", err)
	}

	return InfoResponse{
		Type:    LlmTypeOpenAIAzure,
		IsLocal: false,
		Model: LlmModel{
			Name:    envVars[modelEnvVar].value,
			Version: envVars[versionEnvVar].value,
		},
		Url: envVars[urlEnvVar].value,
	}, nil
}
