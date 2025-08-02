package azd

import (
	"context"

	"github.com/azure/azure-dev/cli/azd/internal/agent/tools/azd/prompts"
	"github.com/tmc/langchaingo/tools"
)

var _ tools.Tool = &AzdDockerGenerationTool{}

type AzdDockerGenerationTool struct {
}

func (t *AzdDockerGenerationTool) Name() string {
	return "azd_docker_generation"
}

func (t *AzdDockerGenerationTool) Description() string {
	return `
		Generates Dockerfiles and container configurations for Azure Developer CLI (AZD) projects.
		This specialized tool focuses on containerization requirements, creating optimized Dockerfiles
		for different programming languages, and configuring container-specific settings for Azure hosting.

		Input: "./azd-arch-plan.md"
	`
}

func (t *AzdDockerGenerationTool) Call(ctx context.Context, input string) (string, error) {
	return prompts.AzdDockerGenerationPrompt, nil
}
