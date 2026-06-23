// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package cmd

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/azdext"
)

var envNamePattern = regexp.MustCompile(`^[a-z][a-z0-9_]*$`)

func validateEnvName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if !envNamePattern.MatchString(name) {
		return "", fmt.Errorf("invalid environment name %q", name)
	}
	return name, nil
}

func scaffoldRleSession(name string, dest string, image string, force bool) (string, error) {
	envClass := toPascal(name)
	envTitle := toTitle(name)
	sessionDir := filepath.Join(dest, name)
	if entries, err := os.ReadDir(sessionDir); err == nil && len(entries) > 0 && !force {
		return "", &azdext.LocalError{
			Message:    fmt.Sprintf("Directory %q already exists and is not empty.", sessionDir),
			Code:       "rle_session_exists",
			Category:   azdext.LocalErrorCategoryUser,
			Suggestion: "Use --force to overwrite generated files, or choose a different environment name.",
		}
	} else if err != nil && !os.IsNotExist(err) {
		return "", err
	}

	files := map[string]string{
		"models.py":                          modelsTemplate,
		"client.py":                          clientTemplate,
		"Dockerfile":                         dockerfileTemplate,
		"requirements.txt":                   requirementsTemplate,
		rleManifestFile:                      manifestTemplate,
		"README.md":                          readmeTemplate,
		".dockerignore":                      dockerignoreTemplate,
		"rle_runtime.py":                     runtimeTemplate,
		"server/" + name + "_environment.py": environmentTemplate,
		"server/app.py":                      appTemplate,
		"server/__init__.py":                 "",
	}

	replacements := map[string]string{
		"__ENV_NAME__":  name,
		"__ENV_CLASS__": envClass,
		"__ENV_TITLE__": envTitle,
		"__IMAGE__":     image,
	}

	for relativePath, content := range files {
		for token, value := range replacements {
			content = strings.ReplaceAll(content, token, value)
		}
		content = strings.ReplaceAll(content, "\n", "\r\n")
		fullPath := filepath.Join(sessionDir, filepath.FromSlash(relativePath))
		if err := os.MkdirAll(filepath.Dir(fullPath), 0755); err != nil {
			return "", err
		}
		if err := os.WriteFile(fullPath, []byte(content), 0600); err != nil {
			return "", err
		}
	}

	return sessionDir, nil
}

func toPascal(name string) string {
	parts := strings.Split(name, "_")
	for i, part := range parts {
		if part == "" {
			continue
		}
		parts[i] = strings.ToUpper(part[:1]) + part[1:]
	}
	return strings.Join(parts, "")
}

func toTitle(name string) string {
	parts := strings.Split(name, "_")
	for i, part := range parts {
		if part == "" {
			continue
		}
		parts[i] = strings.ToUpper(part[:1]) + part[1:]
	}
	return strings.Join(parts, " ")
}

const manifestTemplate = `# RLE environment manifest (OpenEnv-style).
spec_version: 1
name: __ENV_NAME__
type: rle
runtime: fastapi
app: server.app:app
port: 8000
image: __IMAGE__
`

const requirementsTemplate = `fastapi>=0.110
uvicorn>=0.29
pydantic>=2.6
`

const dockerignoreTemplate = `__pycache__/
*.pyc
.venv/
venv/
.git/
.pytest_cache/
`

const modelsTemplate = `"""Data models for the __ENV_TITLE__ environment."""

from pydantic import Field

from rle_runtime import Action, Observation


class __ENV_CLASS__Action(Action):
    """Action for the __ENV_TITLE__ environment."""

    message: str = Field(..., description="Message to send to the environment")


class __ENV_CLASS__Observation(Observation):
    """Observation returned by the __ENV_TITLE__ environment."""

    echoed_message: str = Field(default="", description="The echoed message")
    message_length: int = Field(default=0, description="Length of the echoed message")
`

const environmentTemplate = `"""__ENV_TITLE__ environment implementation."""

from __future__ import annotations

import uuid
from typing import Any, Optional

from rle_runtime import Environment, State

from models import __ENV_CLASS__Action, __ENV_CLASS__Observation


class __ENV_CLASS__Environment(Environment):
    """A simple echo environment.

    Replace the reset/step logic below with your own environment dynamics.
    """

    def __init__(self) -> None:
        self._episode_id: Optional[str] = None
        self._step_count: int = 0

    def reset(
        self,
        seed: Optional[int] = None,
        episode_id: Optional[str] = None,
        **kwargs: Any,
    ) -> __ENV_CLASS__Observation:
        self._episode_id = episode_id or str(uuid.uuid4())
        self._step_count = 0
        return __ENV_CLASS__Observation(
            echoed_message="",
            message_length=0,
            done=False,
            reward=None,
        )

    def step(self, action: __ENV_CLASS__Action, **kwargs: Any) -> __ENV_CLASS__Observation:
        self._step_count += 1
        message = action.message
        return __ENV_CLASS__Observation(
            echoed_message=message,
            message_length=len(message),
            done=False,
            reward=float(len(message)),
        )

    @property
    def state(self) -> State:
        return State(episode_id=self._episode_id, step_count=self._step_count)
`

const appTemplate = `"""FastAPI application entrypoint for the __ENV_TITLE__ environment.

Exposes the OpenEnv-compatible contract:
    POST /reset, POST /step, GET /state, GET /health, GET /metadata, GET /schema

Run locally:
    uvicorn server.app:app --host 0.0.0.0 --port 8000
"""

from rle_runtime import create_app

from models import __ENV_CLASS__Action, __ENV_CLASS__Observation
from server.__ENV_NAME___environment import __ENV_CLASS__Environment

app = create_app(
    __ENV_CLASS__Environment,
    __ENV_CLASS__Action,
    __ENV_CLASS__Observation,
    env_name="__ENV_NAME__",
)


if __name__ == "__main__":
    import uvicorn

    uvicorn.run(app, host="0.0.0.0", port=8000)
`

const clientTemplate = `"""Minimal HTTP client for the __ENV_TITLE__ environment.

Use this for quick local testing against either:
  - the container directly (e.g. http://localhost:32891), or
  - the Foundry data plane gateway, e.g.
    http://localhost:5001/rle/v1.0/projects/<projectId>/environments/<environmentId>/instances/<instanceId>

Example:
    from client import __ENV_CLASS__Client

    base = ("http://localhost:5001/rle/v1.0/projects/proj1"
            "/environments/<environmentId>/instances/<instanceId>")
    c = __ENV_CLASS__Client(base)
    print(c.reset())
    print(c.step({"message": "hello"}))
    print(c.state())
"""

from __future__ import annotations

import json
import urllib.request
from typing import Any, Dict, Optional


class __ENV_CLASS__Client:
    def __init__(self, base_url: str, timeout_s: float = 30.0) -> None:
        self.base_url = base_url.rstrip("/")
        self.timeout_s = timeout_s

    def _post(self, path: str, body: Optional[Dict[str, Any]] = None) -> Dict[str, Any]:
        data = json.dumps(body or {}).encode("utf-8")
        req = urllib.request.Request(
            f"{self.base_url}{path}",
            data=data,
            headers={"Content-Type": "application/json"},
            method="POST",
        )
        with urllib.request.urlopen(req, timeout=self.timeout_s) as resp:
            return json.loads(resp.read().decode("utf-8"))

    def _get(self, path: str) -> Dict[str, Any]:
        req = urllib.request.Request(f"{self.base_url}{path}", method="GET")
        with urllib.request.urlopen(req, timeout=self.timeout_s) as resp:
            return json.loads(resp.read().decode("utf-8"))

    def reset(self, seed: Optional[int] = None, episode_id: Optional[str] = None) -> Dict[str, Any]:
        return self._post("/reset", {"seed": seed, "episode_id": episode_id})

    def step(self, action: Dict[str, Any]) -> Dict[str, Any]:
        return self._post("/step", {"action": action})

    def state(self) -> Dict[str, Any]:
        return self._get("/state")
`

const dockerfileTemplate = `# __ENV_TITLE__ RLE environment image.
FROM python:3.11-slim

WORKDIR /app

# Install dependencies first for better layer caching.
COPY requirements.txt .
RUN pip install --no-cache-dir -r requirements.txt

# Copy the environment code.
COPY . .

# OpenEnv-style environments always listen on port 8000.
EXPOSE 8000

# Container-level health check (pure Python, no curl needed).
HEALTHCHECK --interval=10s --timeout=3s --start-period=5s --retries=5 \
    CMD python -c "import urllib.request,sys; sys.exit(0 if urllib.request.urlopen('http://localhost:8000/health', timeout=2).status==200 else 1)" || exit 1

CMD ["uvicorn", "server.app:app", "--host", "0.0.0.0", "--port", "8000"]
`

const readmeTemplate = `# __ENV_TITLE__ RLE Environment

An OpenEnv-style Reinforcement Learning Environment generated by the ` + "`rle`" + ` CLI.

## Layout

| File | Purpose |
| --- | --- |
| ` + "`rle_runtime.py`" + ` | Self-contained OpenEnv-compatible runtime (base models + FastAPI factory). |
| ` + "`models.py`" + ` | ` + "`__ENV_CLASS__Action`" + ` / ` + "`__ENV_CLASS__Observation`" + ` data models. |
| ` + "`server/__ENV_NAME___environment.py`" + ` | ` + "`__ENV_CLASS__Environment`" + ` — your ` + "`reset`" + `/` + "`step`" + `/` + "`state`" + ` logic. |
| ` + "`server/app.py`" + ` | FastAPI app exposing ` + "`/reset`" + `, ` + "`/step`" + `, ` + "`/state`" + `, ` + "`/health`" + `. |
| ` + "`client.py`" + ` | Minimal HTTP client for testing. |
| ` + "`Dockerfile`" + ` | Builds the environment image (listens on ` + "`:8000`" + `). |
| ` + "`rle.yaml`" + ` | Environment manifest. |

## Run locally (no Docker)

` + "```bash" + `
pip install -r requirements.txt
uvicorn server.app:app --host 0.0.0.0 --port 8000
# in another shell:
curl -X POST localhost:8000/reset
curl -X POST localhost:8000/step -H 'content-type: application/json' \
     -d '{"action": {"message": "hello"}}'
curl localhost:8000/state
` + "```" + `

## Deploy into Foundry

` + "```bash" + `
rle deploy --project <projectId> --path . --name __ENV_NAME__ \
    --control-plane http://localhost:5000
` + "```" + `

This builds the image, ensures an environment definition exists via the control
plane, deploys an instance, and prints the data plane endpoint you can call
` + "`reset`" + `/` + "`step`" + `/` + "`state`" + ` against.
`

const runtimeTemplate = `# rle_runtime.py
#
# Self-contained, OpenEnv-compatible runtime for RLE environment containers.
#
# This single file provides:
#   - Base data models: Action, Observation, State
#   - The Environment abstract base class (reset / step / state)
#   - create_app(): a FastAPI factory exposing the OpenEnv simulation contract:
#         POST /reset   -> {observation, reward, done, metadata}
#         POST /step    -> {observation, reward, done, metadata}
#         GET  /state   -> State as dict
#         GET  /health  -> {status: "healthy"}
#         GET  /metadata, GET /schema
#
# It intentionally has NO dependency on the ` + "`openenv`" + ` package so that generated
# environments are fully standalone and runnable with only fastapi/uvicorn/pydantic.
from __future__ import annotations

import logging
import socket
from abc import ABC, abstractmethod
from typing import Any, Dict, Optional, Type

from fastapi import Body, FastAPI
from pydantic import BaseModel, ConfigDict, Field

# The container's hostname is, by default, the short Docker container id (we do
# not pass --hostname), so it can be used directly with ` + "`docker logs <id>`" + `.
CONTAINER_ID = socket.gethostname()


def _make_logger(env_name: str) -> logging.Logger:
    """A stdout logger whose lines are captured by ` + "`docker logs`" + `."""
    logger = logging.getLogger(f"rle.{env_name}")
    if not logger.handlers:
        handler = logging.StreamHandler()
        handler.setFormatter(logging.Formatter("%(asctime)s [rle] %(message)s"))
        logger.addHandler(handler)
        logger.setLevel(logging.INFO)
        logger.propagate = False
    return logger


# ---------------------------------------------------------------------------
# Core data models (mirror openenv.core.env_server.types)
# ---------------------------------------------------------------------------
class Action(BaseModel):
    """Base class for all environment actions."""

    model_config = ConfigDict(extra="forbid", arbitrary_types_allowed=True)
    metadata: Dict[str, Any] = Field(default_factory=dict)


class Observation(BaseModel):
    """Base class for all environment observations."""

    model_config = ConfigDict(extra="forbid", arbitrary_types_allowed=True)
    done: bool = Field(default=False, description="Whether the episode has terminated")
    reward: Optional[float] = Field(default=None, description="Reward from last action")
    metadata: Dict[str, Any] = Field(default_factory=dict)


class State(BaseModel):
    """Base class for environment state."""

    model_config = ConfigDict(extra="allow", arbitrary_types_allowed=True)
    episode_id: Optional[str] = Field(default=None)
    step_count: int = Field(default=0)


class Environment(ABC):
    """Gym-style environment base class.

    Subclasses implement reset(), step(), and the ` + "`state`" + ` property.
    """

    @abstractmethod
    def reset(
        self, seed: Optional[int] = None, episode_id: Optional[str] = None, **kwargs: Any
    ) -> Observation:
        """Reset the environment and return the initial observation."""

    @abstractmethod
    def step(self, action: Action, **kwargs: Any) -> Observation:
        """Apply an action and return the resulting observation."""

    @property
    @abstractmethod
    def state(self) -> State:
        """Return the current environment state."""


# ---------------------------------------------------------------------------
# Wire models (mirror the OpenEnv HTTP contract)
# ---------------------------------------------------------------------------
class ResetRequest(BaseModel):
    model_config = ConfigDict(extra="allow")
    seed: Optional[int] = None
    episode_id: Optional[str] = None


class StepRequest(BaseModel):
    model_config = ConfigDict(extra="allow")
    action: Dict[str, Any]
    timeout_s: Optional[float] = None
    request_id: Optional[str] = None


def _serialize_observation(obs: Observation) -> Dict[str, Any]:
    """Convert an Observation into the OpenEnv wire format."""
    obs_dict = obs.model_dump(exclude={"reward", "done"})
    result: Dict[str, Any] = {
        "observation": obs_dict,
        "reward": obs.reward,
        "done": obs.done,
    }
    if obs.metadata:
        result["metadata"] = obs.metadata
    return result


def create_app(
    environment_cls: Type[Environment],
    action_cls: Type[Action],
    observation_cls: Type[Observation],
    env_name: str = "rle-env",
) -> FastAPI:
    """Build a FastAPI app exposing reset/step/state for the given environment.

    A single environment instance is created at startup (single-session
    simulation mode), which matches how the data plane proxies stateless calls.
    """
    app = FastAPI(title=f"{env_name} (RLE)", version="1.0.0")
    env: Environment = environment_cls()
    log = _make_logger(env_name)

    @app.get("/health", tags=["Health"])
    def health() -> Dict[str, str]:
        return {"status": "healthy"}

    @app.get("/metadata", tags=["Environment Info"])
    def metadata() -> Dict[str, Any]:
        return {
            "name": env_name,
            "description": f"{env_name} RLE environment",
            "version": "1.0.0",
            "container": CONTAINER_ID,
        }

    @app.get("/schema", tags=["Schema"])
    def schema() -> Dict[str, Any]:
        return {
            "action": action_cls.model_json_schema(),
            "observation": observation_cls.model_json_schema(),
            "state": State.model_json_schema(),
        }

    @app.post("/reset", tags=["Environment Control"])
    def reset(request: ResetRequest = Body(default_factory=ResetRequest)) -> Dict[str, Any]:
        obs = env.reset(seed=request.seed, episode_id=request.episode_id)
        st = env.state
        log.info(
            "[%s] RESET episode=%s seed=%s -> done=%s reward=%s",
            CONTAINER_ID, st.episode_id, request.seed, obs.done, obs.reward,
        )
        return _serialize_observation(obs)

    @app.post("/step", tags=["Environment Control"])
    def step(request: StepRequest) -> Dict[str, Any]:
        action = action_cls.model_validate(request.action)
        obs = env.step(action)
        st = env.state
        log.info(
            "[%s] STEP  episode=%s step=%s action=%s -> reward=%s done=%s",
            CONTAINER_ID, st.episode_id, st.step_count, request.action, obs.reward, obs.done,
        )
        return _serialize_observation(obs)

    @app.get("/state", tags=["State Management"])
    def state() -> Dict[str, Any]:
        return env.state.model_dump()

    return app
`
