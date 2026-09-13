// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

type AgentScaffoldKind string

const (
	AgentScaffoldKindHostedAgent AgentScaffoldKind = "hosted_agent"
	AgentScaffoldKindBYOH        AgentScaffoldKind = "byoh"
)

type AgentScaffoldOptions struct {
	Kind            AgentScaffoldKind
	EnvironmentName string
	AgentName       string
	AgentVersion    string
	BaseURL         string
}

func CreateRleAgentScaffold(options AgentScaffoldOptions, dest string, force bool) (string, error) {
	normalized, err := normalizeAgentScaffoldOptions(options)
	if err != nil {
		return "", err
	}

	sessionDir, err := createRleSessionDir(normalized.EnvironmentName, dest, force)
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(filepath.Join(sessionDir, "server"), 0750); err != nil {
		return "", err
	}

	files := []struct {
		path    string
		content string
	}{
		{path: "rle.toml", content: renderAgentRleConfig(normalized)},
		{path: "Dockerfile", content: agentDockerfile},
		{path: filepath.Join("server", "__init__.py"), content: ""},
		{path: filepath.Join("server", "env.py"), content: renderAgentServer(normalized)},
	}
	for _, file := range files {
		if err := os.WriteFile(filepath.Join(sessionDir, file.path), []byte(file.content), 0644); err != nil {
			return "", err
		}
	}
	return sessionDir, nil
}

func normalizeAgentScaffoldOptions(options AgentScaffoldOptions) (AgentScaffoldOptions, error) {
	environmentName, err := ValidateEnvironmentName(options.EnvironmentName)
	if err != nil {
		return AgentScaffoldOptions{}, &azdext.LocalError{
			Message:    err.Error(),
			Code:       "rle_invalid_environment_name",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Use snake_case starting with a letter, for example support_agent.",
		}
	}
	options.EnvironmentName = environmentName
	options.AgentName = strings.TrimSpace(options.AgentName)
	options.AgentVersion = strings.TrimSpace(options.AgentVersion)

	switch options.Kind {
	case AgentScaffoldKindHostedAgent:
		if options.AgentName == "" {
			return AgentScaffoldOptions{}, requiredAgentScaffoldFieldError(
				"agent name",
				"rle_agent_name_required",
				"Provide --agent-name or select an agent name when prompted.",
			)
		}
		if options.AgentVersion == "" {
			return AgentScaffoldOptions{}, requiredAgentScaffoldFieldError(
				"agent version",
				"rle_agent_version_required",
				"Provide --agent-version or select an agent version when prompted.",
			)
		}
	case AgentScaffoldKindBYOH:
		baseURL, err := normalizeAgentBaseURL(options.BaseURL)
		if err != nil {
			return AgentScaffoldOptions{}, err
		}
		options.BaseURL = baseURL
	default:
		return AgentScaffoldOptions{}, &azdext.LocalError{
			Message:    fmt.Sprintf("Unsupported RLE agent scaffold type %q.", options.Kind),
			Code:       "rle_agent_scaffold_type_invalid",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Choose Hosted Agent or BYOH.",
		}
	}
	return options, nil
}

func requiredAgentScaffoldFieldError(name string, code string, suggestion string) error {
	return &azdext.LocalError{
		Message:    fmt.Sprintf("An %s is required for this RLE scaffold.", name),
		Code:       code,
		Category:   azdext.LocalErrorCategoryUser,
		Suggestion: suggestion,
	}
}

func normalizeAgentBaseURL(value string) (string, error) {
	value = strings.TrimSpace(value)
	parsed, err := url.ParseRequestURI(value)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" ||
		(strings.ToLower(parsed.Scheme) != "https" && strings.ToLower(parsed.Scheme) != "http") ||
		parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", &azdext.LocalError{
			Message:    "The BYOH agent base URL must be an absolute HTTP or HTTPS URL without credentials, query, or fragment.",
			Code:       "rle_agent_base_url_invalid",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Use a URL such as https://agent.example.com.",
		}
	}
	return strings.TrimRight(value, "/"), nil
}

func renderAgentRleConfig(options AgentScaffoldOptions) string {
	var builder strings.Builder
	builder.WriteString("[rle]\n")
	builder.WriteString("name = ")
	builder.WriteString(tomlString(options.EnvironmentName))
	builder.WriteString("\nkind = ")
	builder.WriteString(tomlString(string(options.Kind)))
	builder.WriteString("\n\n[agent]\n")
	switch options.Kind {
	case AgentScaffoldKindHostedAgent:
		builder.WriteString("name = ")
		builder.WriteString(tomlString(options.AgentName))
		builder.WriteString("\nversion = ")
		builder.WriteString(tomlString(options.AgentVersion))
	case AgentScaffoldKindBYOH:
		builder.WriteString("base_url = ")
		builder.WriteString(tomlString(options.BaseURL))
	}
	builder.WriteString("\n")
	return builder.String()
}

func tomlString(value string) string {
	replacer := strings.NewReplacer(
		`\`, `\\`,
		`"`, `\"`,
		"\b", `\b`,
		"\t", `\t`,
		"\n", `\n`,
		"\f", `\f`,
		"\r", `\r`,
	)
	return `"` + replacer.Replace(value) + `"`
}

const agentDockerfile = `FROM python:3.12-slim

ARG PIP_INDEX_URL=https://pypi.org/simple
ARG PIP_FALLBACK_INDEX_URL=https://packagefeedproxy.microsoft.io/pypi/simple/

WORKDIR /app

RUN if [ -n "${PIP_FALLBACK_INDEX_URL}" ] && \
       ! pip download --no-deps --quiet --dest /tmp/idxprobe six >/dev/null 2>&1; then \
        echo "Primary index unreachable; falling back to ${PIP_FALLBACK_INDEX_URL}"; \
        export PIP_INDEX_URL="${PIP_FALLBACK_INDEX_URL}"; \
    fi; \
    rm -rf /tmp/idxprobe; \
    pip install --no-cache-dir openenv

COPY server/ /app/server/

RUN python -c "import server.env as m; print('RLE OpenEnv server OK:', m.app)"

ENV ENABLE_WEB_INTERFACE=true

EXPOSE 8000

CMD ["uvicorn", "server.env:app", "--host", "0.0.0.0", "--port", "8000"]
`

const agentServerTemplate = `"""Starter RLE harness for a %s target."""

from __future__ import annotations

from typing import Any, Optional
from uuid import uuid4

from pydantic import Field

from openenv.core.env_server.http_server import create_app
from openenv.core.env_server.interfaces import Environment
from openenv.core.env_server.types import Action, EnvironmentMetadata, Observation, State

TARGET_KIND = %s
AGENT_NAME = %s
AGENT_VERSION = %s
AGENT_BASE_URL = %s

class AgentAction(Action):
    """One rollout action supplied by the configured agent."""

    message: str = Field(default="", description="Agent response or action text.")
    tool_calls: list[dict[str, Any]] = Field(
        default_factory=list,
        description="Optional parsed tool calls from the agent response.",
    )


class AgentObservation(Observation):
    """Agent-visible messages emitted by the harness."""

    messages: list[dict[str, Any]] = Field(default_factory=list)


class AgentHarnessEnvironment(Environment[AgentAction, AgentObservation, State]):
    """One isolated RLE episode served through OpenEnv."""

    def __init__(self) -> None:
        super().__init__()
        self._state = State(episode_id=None, step_count=0)
        self._task_data: dict[str, Any] = {}

    def reset(
        self,
        seed: Optional[int] = None,
        episode_id: Optional[str] = None,
        **task_data: Any,
    ) -> AgentObservation:
        del seed
        self._task_data = task_data
        self._state = State(episode_id=episode_id or str(uuid4()), step_count=0)
        agent_input = task_data.get("agent_input", task_data.get("input"))
        messages: list[dict[str, Any]] = []
        if agent_input is not None:
            messages.append({"role": "user", "content": str(agent_input)})
        # TODO: initialize task-specific mock tools and grading state here.
        return AgentObservation(done=False, reward=None, messages=messages)

    def step(
        self,
        action: AgentAction,
        timeout_s: Optional[float] = None,
        **kwargs: Any,
    ) -> AgentObservation:
        del timeout_s, kwargs
        self._state.step_count += 1
        # TODO: exercise agent tool calls against harness mocks before grading.
        return AgentObservation(
            done=True,
            reward=self.grade(action),
            messages=[],
            metadata={"step": self._state.step_count},
        )

    def grade(self, action: AgentAction) -> float:
        del action
        # TODO: score the final agent response and recorded mock-tool effects.
        return 0.0

    @property
    def state(self) -> State:
        return self._state

    def get_metadata(self) -> EnvironmentMetadata:
        return EnvironmentMetadata(
            name=%s,
            description="RLE harness for a " + TARGET_KIND + " target.",
            version="0.1.0",
        )


# OpenEnv owns /health, /schema, /metadata, /ws, and the optional /web UI.
app = create_app(
    AgentHarnessEnvironment,
    AgentAction,
    AgentObservation,
    env_name=%s,
    max_concurrent_envs=1,
)


@app.post("/tools/example")
async def example_tool(arguments: dict[str, Any]) -> dict[str, Any]:
    # TODO: replace this with mocks that preserve the production tool contracts.
    return {"ok": True, "arguments": arguments}


@app.post("/grade")
async def grade_rollout(rollout: dict[str, Any]) -> dict[str, Any]:
    del rollout
    # TODO: score a completed rollout for RLE's grader integration.
    return {"reward": 0.0, "reason": "Implement the RLE grader for this agent."}
`

func renderAgentServer(options AgentScaffoldOptions) string {
	agentName := "None"
	agentVersion := "None"
	baseURL := "None"
	if options.AgentName != "" {
		agentName = strconv.Quote(options.AgentName)
	}
	if options.AgentVersion != "" {
		agentVersion = strconv.Quote(options.AgentVersion)
	}
	if options.BaseURL != "" {
		baseURL = strconv.Quote(options.BaseURL)
	}
	return fmt.Sprintf(
		agentServerTemplate,
		strings.ReplaceAll(string(options.Kind), "_", " "),
		strconv.Quote(string(options.Kind)),
		agentName,
		agentVersion,
		baseURL,
		strconv.Quote(options.EnvironmentName),
		strconv.Quote(options.EnvironmentName),
	)
}
