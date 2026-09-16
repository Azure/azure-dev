// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package project

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

type HarnessScaffoldOptions struct {
	EnvironmentName string
	RleVersion      string
	Type            RleType
	Subtype         RleSubtype
	AgentName       string
	AgentVersion    string
	BaseURL         string
}

func CreateRleHarnessScaffold(options HarnessScaffoldOptions, dest string, force bool) (string, error) {
	config, err := normalizeHarnessScaffoldOptions(options)
	if err != nil {
		return "", err
	}

	sessionDir, err := createRleSessionDir(config.Rle.Name, dest, force)
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
		{path: "Dockerfile", content: harnessDockerfile},
		{path: filepath.Join("server", "__init__.py"), content: ""},
		{path: filepath.Join("server", "env.py"), content: renderHarnessServer(config.Rle)},
	}
	for _, file := range files {
		if err := os.WriteFile(filepath.Join(sessionDir, file.path), []byte(file.content), 0644); err != nil {
			return "", err
		}
	}
	if err := WriteRleConfig(sessionDir, config); err != nil {
		return "", err
	}
	return sessionDir, nil
}

func normalizeHarnessScaffoldOptions(options HarnessScaffoldOptions) (RleConfig, error) {
	if strings.TrimSpace(options.RleVersion) == "" {
		options.RleVersion = DefaultRleVersion
	}
	schemaVersion := CurrentRleManifestSchemaVersion
	manifest := RleManifest{
		Name:    options.EnvironmentName,
		Version: options.RleVersion,
		Type:    options.Type,
		Subtype: options.Subtype,
	}
	if strings.TrimSpace(options.AgentName) != "" {
		agentName := options.AgentName
		manifest.AgentName = &agentName
	}
	if strings.TrimSpace(options.AgentVersion) != "" {
		agentVersion := options.AgentVersion
		manifest.AgentVersion = &agentVersion
	}
	if strings.TrimSpace(options.BaseURL) != "" {
		baseURL := options.BaseURL
		manifest.BaseURL = &baseURL
	}

	config, err := NormalizeRleConfig(RleConfig{
		SchemaVersion: &schemaVersion,
		Rle:           manifest,
	})
	if err != nil {
		return RleConfig{}, err
	}
	if config.Rle.Type != RleTypeHarness {
		return RleConfig{}, &azdext.LocalError{
			Message:    fmt.Sprintf("RLE harness scaffolds require type Harness, got %q.", config.Rle.Type),
			Code:       "rle_harness_scaffold_type_invalid",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: `Set type to "Harness" and select HostedAgent or BYOH.`,
		}
	}
	return config, nil
}

const harnessDockerfile = `FROM python:3.12-slim

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

const harnessServerTemplate = `"""Starter RLE harness for a %s/%s target."""

from __future__ import annotations

from typing import Any, Optional
from uuid import uuid4

from pydantic import Field

from openenv.core.env_server.http_server import create_app
from openenv.core.env_server.interfaces import Environment
from openenv.core.env_server.types import Action, EnvironmentMetadata, Observation, State

RLE_TYPE = %s
RLE_SUBTYPE = %s
AGENT_NAME = %s
AGENT_VERSION = %s
HARNESS_BASE_URL = %s

class HarnessAction(Action):
    """One rollout action supplied to the configured harness."""

    message: str = Field(default="", description="Model response or action text.")
    tool_calls: list[dict[str, Any]] = Field(
        default_factory=list,
        description="Optional parsed tool calls from the agent response.",
    )


class HarnessObservation(Observation):
    """Rollout-visible messages emitted by the harness."""

    messages: list[dict[str, Any]] = Field(default_factory=list)


class RleHarnessEnvironment(Environment[HarnessAction, HarnessObservation, State]):
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
    ) -> HarnessObservation:
        del seed
        self._task_data = task_data
        self._state = State(episode_id=episode_id or str(uuid4()), step_count=0)
        agent_input = task_data.get("agent_input", task_data.get("input"))
        messages: list[dict[str, Any]] = []
        if agent_input is not None:
            messages.append({"role": "user", "content": str(agent_input)})
        # TODO: initialize task-specific mock tools and grading state here.
        return HarnessObservation(done=False, reward=None, messages=messages)

    def step(
        self,
        action: HarnessAction,
        timeout_s: Optional[float] = None,
        **kwargs: Any,
    ) -> HarnessObservation:
        del timeout_s, kwargs
        self._state.step_count += 1
        # TODO: exercise model tool calls against harness mocks before grading.
        return HarnessObservation(
            done=True,
            reward=self.grade(action),
            messages=[],
            metadata={"step": self._state.step_count},
        )

    def grade(self, action: HarnessAction) -> float:
        del action
        # TODO: score the final agent response and recorded mock-tool effects.
        return 0.0

    @property
    def state(self) -> State:
        return self._state

    def get_metadata(self) -> EnvironmentMetadata:
        return EnvironmentMetadata(
            name=%s,
            description="RLE harness for a " + RLE_TYPE + "/" + RLE_SUBTYPE + " target.",
            version="0.1.0",
        )


# OpenEnv owns /health, /schema, /metadata, /ws, and the optional /web UI.
app = create_app(
    RleHarnessEnvironment,
    HarnessAction,
    HarnessObservation,
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
    return {"reward": 0.0, "reason": "Implement the RLE grader for this harness."}
`

func renderHarnessServer(manifest RleManifest) string {
	agentName := "None"
	agentVersion := "None"
	baseURL := "None"
	if manifest.AgentName != nil {
		agentName = strconv.Quote(*manifest.AgentName)
	}
	if manifest.AgentVersion != nil {
		agentVersion = strconv.Quote(*manifest.AgentVersion)
	}
	if manifest.BaseURL != nil {
		baseURL = strconv.Quote(*manifest.BaseURL)
	}
	return fmt.Sprintf(
		harnessServerTemplate,
		manifest.Type,
		manifest.Subtype,
		strconv.Quote(string(manifest.Type)),
		strconv.Quote(string(manifest.Subtype)),
		agentName,
		agentVersion,
		baseURL,
		strconv.Quote(manifest.Name),
		strconv.Quote(manifest.Name),
	)
}
