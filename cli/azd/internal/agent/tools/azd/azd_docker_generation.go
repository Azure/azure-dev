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
	return `Returns instructions for generating optimized Dockerfiles and container configurations for containerizable services in AZD projects. The LLM agent should execute these instructions using available tools.

Use this tool when:
- Architecture planning identified services requiring containerization
- azd-arch-plan.md shows Container Apps or AKS as selected hosting platform
- Need Dockerfiles for microservices, APIs, or containerized web applications
- Ready to implement containerization strategy

Input: "./azd-arch-plan.md"`
}

func (t *AzdDockerGenerationTool) Call(ctx context.Context, input string) (string, error) {
	return prompts.AzdDockerGenerationPrompt, nil
}
