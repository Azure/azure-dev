#!/usr/bin/env python3
# Copyright (c) Microsoft Corporation. All rights reserved.
# Licensed under the MIT License.

"""Fork-only candidate proof, using release binaries through the real azd host.

No Azure login, deployment, dataset registration, evaluation run, or quality gate
is performed. Only synthetic local authoring and pre-network errors are tested.
Seed validation compares authored project files and private configuration before
and after failures. Piped stdin is not evidence of interactive correction.
Update candidate.json from the publisher's immutable release, never from latest.
The output directory contains only explicitly selected, sanitized evidence.
"""

import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import re
import subprocess
import tarfile
import tempfile
import urllib.parse
import urllib.request
import zipfile


def require(condition, message):
    if not condition:
        raise AssertionError(message)


def sha256(data):
    return hashlib.sha256(data).hexdigest()


def sanitize(text, root):
    text = text.replace(str(root), "<isolated-work>")
    text = text.replace(str(root).replace("\\", "/"), "<isolated-work>")

    def clean_url(match):
        url = urllib.parse.urlsplit(match.group(0))
        host = url.netloc.rsplit("@", 1)[-1]
        return urllib.parse.urlunsplit((url.scheme, host, url.path, "", ""))

    return re.sub(r"https?://[^\s<>\"']+", clean_url, text)


def write_json(path, value):
    path.write_text(json.dumps(value, indent=2) + "\n", encoding="utf-8")


def snapshot_tree(directory):
    return {
        str(path.relative_to(directory)): (
            {"directory": True} if path.is_dir() else {"sha256": sha256(path.read_bytes())}
        )
        for path in directory.rglob("*")
    }


def download(url, digest, directory):
    parsed = urllib.parse.urlsplit(url)
    require(
        parsed.scheme == "https" and parsed.hostname == "github.com"
        and not parsed.username and not parsed.query and not parsed.fragment,
        "Artifacts must use credential-free GitHub release URLs",
    )
    with urllib.request.urlopen(url, timeout=120) as response:
        data = response.read()
    require(sha256(data) == digest, f"SHA-256 mismatch for {parsed.path}")
    path = directory / Path(parsed.path).name
    path.write_bytes(data)
    return path


def binary_from_archive(path, name):
    if path.suffix == ".zip":
        with zipfile.ZipFile(path) as archive:
            matches = [entry for entry in archive.namelist() if Path(entry).name == name]
            require(len(matches) == 1, f"Expected exactly one {name} in {path.name}")
            return archive.read(matches[0])
    with tarfile.open(path, "r:gz") as archive:
        matches = [entry for entry in archive.getmembers() if Path(entry.name).name == name]
        require(len(matches) == 1 and matches[0].isfile(), f"Expected one binary {name}")
        with archive.extractfile(matches[0]) as entry:
            return entry.read()


class Proof:
    def __init__(self, root, output, pin):
        self.root, self.output, self.pin = root, output, pin
        self.commands = []
        self.checks = []
        self.platform = "windows/amd64" if os.name == "nt" else "linux/amd64"
        require(platform.machine().lower() in ("amd64", "x86_64"), "Requires an x64 host")
        self.azd = root / ("azd.exe" if os.name == "nt" else "azd")
        # Do not inherit user tokens, az/azd caches, endpoints, or GitHub credentials.
        self.env = {
            key: value for key, value in os.environ.items()
            if key.upper() in ("PATH", "SYSTEMROOT", "WINDIR", "COMSPEC", "PATHEXT", "LANG")
        }
        for name in ("home", "config", "azure", "temp", "downloads"):
            (root / name).mkdir()
        self.env.update({
            "HOME": str(root / "home"),
            "USERPROFILE": str(root / "home"),
            "AZD_CONFIG_DIR": str(root / "config"),
            "AZURE_CONFIG_DIR": str(root / "azure"),
            "TMP": str(root / "temp"),
            "TEMP": str(root / "temp"),
            "TMPDIR": str(root / "temp"),
            "AZURE_DEV_COLLECT_TELEMETRY": "no",
            "AZD_FORCE_TTY": "false",
            "NO_COLOR": "1",
            "CI": "true",
            "PATH": str(root) + os.pathsep + self.env.get("PATH", ""),
        })

    def run(self, name, args, cwd=None, failure=None, json_output=False, timeout=60):
        argv = [str(self.azd), *args, "--no-prompt"]
        result = subprocess.run(
            argv, cwd=cwd or self.root, env=self.env, stdin=subprocess.DEVNULL,
            capture_output=True, text=True, encoding="utf-8", timeout=timeout,
        )
        self.commands.append({
            "name": name,
            "command": ["azd", *[sanitize(arg, self.root) for arg in argv[1:]]],
            "exitCode": result.returncode,
            "expectedFailure": failure is not None,
            "stdout": sanitize(result.stdout, self.root),
            "stderr": sanitize(result.stderr, self.root),
        })
        require(
            result.returncode != 0 if failure else result.returncode == 0,
            f"{name}: unexpected exit code {result.returncode}: "
            + sanitize(result.stdout + result.stderr, self.root),
        )
        value = json.loads(result.stdout) if json_output else result.stdout
        if failure:
            require(isinstance(value, dict), f"{name}: missing JSON error document")
            message = value.get("error", {}).get("message", "")
            require(message and re.search(failure, message, re.I), f"{name}: wrong error: {message}")
        self.checks.append(name)
        print(f"PASS: {name}", flush=True)
        return value

    def install(self):
        downloads = self.root / "downloads"
        azd_pin = self.pin["azd"]
        artifact = azd_pin["artifacts"][self.platform]
        url = (
            "https://github.com/Azure/azure-dev/releases/download/"
            f"azure-dev-cli_{azd_pin['version']}/{artifact['file']}"
        )
        archive = download(url, artifact["sha256"], downloads)
        binary_name = "azd-windows-amd64.exe" if os.name == "nt" else "azd-linux-amd64"
        binary = binary_from_archive(archive, binary_name)
        self.azd.write_bytes(binary)
        self.azd.chmod(0o700)
        version = self.run("azd version", ["version"])
        require(
            re.search(rf"azd version {re.escape(azd_pin['version'])}(?:\s|$)", version),
            "The azd binary version does not match the pin",
        )
        base = (
            f"https://github.com/{self.pin['releaseRepository']}/releases/download/"
            f"{self.pin['releaseTag']}/"
        )
        registry_path = download(base + "registry.json", self.pin["registrySha256"], downloads)
        registry = json.loads(registry_path.read_text(encoding="utf-8-sig"))
        require(
            {ext["id"] for ext in registry["extensions"]} == set(self.pin["extensions"]),
            "The release registry must contain exactly the two candidate extensions",
        )
        expected_binaries = {}
        for extension in registry["extensions"]:
            extension_pin = self.pin["extensions"][extension["id"]]
            versions = [
                version for version in extension["versions"]
                if version["version"] == extension_pin["version"]
            ]
            require(len(versions) == 1, "Pinned extension version must occur exactly once")
            version = versions[0]
            artifact = version["artifacts"][self.platform]
            digest = extension_pin["artifacts"][self.platform]
            require(artifact["checksum"] == {"algorithm": "sha256", "value": digest},
                    "Registry checksum differs from the independent candidate pin")
            require(artifact["url"].startswith(base), "Artifact is not from the pinned release")
            archive = download(artifact["url"], digest, downloads)
            expected_binaries[extension["id"]] = (
                artifact["entryPoint"],
                sha256(binary_from_archive(archive, artifact["entryPoint"])),
            )
            # Only the URL changes. azd independently verifies the original archive digest.
            artifact["url"] = str(archive)
            version["artifacts"] = {self.platform: artifact}
            extension["versions"] = versions
        local_registry = self.root / "verified-registry.json"
        write_json(local_registry, registry)
        self.run("register verified feed", [
            "extension", "source", "add", "--name", "candidate-proof",
            "--type", "file", "--location", str(local_registry),
        ])
        for extension_id, pin in self.pin["extensions"].items():
            self.run(f"install {extension_id}", [
                "extension", "install", extension_id, "--source", "candidate-proof",
                "--version", pin["version"],
            ], timeout=120)
            entry, digest = expected_binaries[extension_id]
            installed = self.root / "config" / "extensions" / extension_id / entry
            require(sha256(installed.read_bytes()) == digest, "Installed binary differs from release")
            info = self.run(
                f"{extension_id} version JSON",
                ["ai", pin["command"], "version", "--output", "json"], json_output=True,
            )
            require(info == {"name": extension_id, "version": pin["version"]},
                    "Installed binary reports an unexpected version")

    def exercise(self):
        # Only host-local gRPC is needed after installation. Block remote HTTP(S),
        # including accidental catalogue/auth lookups, without a service mock.
        self.env.update({
            "HTTP_PROXY": "http://127.0.0.1:9",
            "HTTPS_PROXY": "http://127.0.0.1:9",
            "NO_PROXY": "localhost,127.0.0.1,::1",
        })
        for command in (
            ["eval"], ["eval", "init"], ["eval", "generate"], ["eval", "create"],
            ["eval", "run", "start"], ["eval", "run", "output", "export"],
            ["eval", "evaluator", "create"], ["dataset"], ["dataset", "create"],
            ["dataset", "update"], ["dataset", "download"], ["dataset", "delete"],
            ["dataset", "versions", "list"],
        ):
            text = self.run("help " + " ".join(command), ["ai", *command, "--help"])
            require("Usage" in text, "Help did not render")
            if command == ["eval", "init"] and self.pin["conversationModes"]:
                for flag in ("--conversation-mode", "--simulation-model", "--num-conversations",
                             "--max-turns", "--judge-model", "--no-prompt"):
                    require(flag in text, f"Init help lost the documented {flag} flag")
                normalized = " ".join(text.split())
                require("independent of the generation and judge models" in normalized,
                        "Init help must distinguish simulator, generation and judge models")

        project = self.root / "synthetic-project"
        project.mkdir()
        project_definition = (
            "name: offline-proof\nservices:\n"
            "  ci-project:\n    host: azure.ai.project\n"
            "  ci-agent:\n    host: azure.ai.agent\n"
        )
        (project / "azure.yaml").write_text(project_definition, encoding="utf-8")
        data = project / "golden.jsonl"
        data.write_text('{"query":"What is two plus two?","response":"4","ground_truth":"4"}\n',
                        encoding="utf-8")
        conversation = project / "conversation.jsonl"
        conversation.write_text(
            '{"messages":[{"role":"user","content":"Hello"},'
            '{"role":"assistant","content":"Hello! How can I help?"}]}\n', encoding="utf-8",
        )
        malformed = project / "malformed.jsonl"
        malformed.write_text('{"query":"valid first row"}\nnot-json\n', encoding="utf-8")
        empty = project / "empty.jsonl"
        empty.write_text("", encoding="utf-8")

        base = ["ai", "eval", "init", "--target", "ci-agent", "--judge-model", "ci-judge"]
        missing_inputs = self.root / "missing-inputs"
        missing_inputs.mkdir()
        (missing_inputs / "azure.yaml").write_text(project_definition, encoding="utf-8")
        self.run("unattended init requires dataset", base + [
            "--source", "dataset", "--evaluator", "builtin.task_adherence", "--output", "json",
        ], missing_inputs, failure="dataset", json_output=True)
        require(not (missing_inputs / "evals").exists(), "Missing inputs created a partial scaffold")
        require((missing_inputs / "azure.yaml").read_text(encoding="utf-8") == project_definition,
                "Missing inputs changed the root project")

        turn = base + [
            "--name", "ci-turn", "--source", "dataset", "--dataset", str(data),
            "--evaluator", "builtin.task_adherence", "--output", "json",
        ]
        info = self.run("author turn eval JSON", turn, project, json_output=True)
        require(
            info["eval"] == "ci-turn" and info["source"] == "dataset"
            and info["target"] == "ci-agent" and info["judgeModel"] == "ci-judge"
            and info["evaluators"] == ["builtin.task_adherence"],
            "Scaffold JSON does not preserve explicit inputs",
        )
        config = Path(info["evalConfig"])
        if not config.is_absolute():
            config = project / config
        require(config.is_file(), "Scaffold did not write its declared config")
        turn_yaml = config.read_text(encoding="utf-8")
        require("ci-turn" in turn_yaml and "evaluation_level: turn" in turn_yaml,
                "Turn evaluation was not written to disk")
        require("azure.ai.eval" in (project / "azure.yaml").read_text(encoding="utf-8"),
                "Root project was not wired to the evaluation service")

        before = (config.read_bytes(), (project / "azure.yaml").read_bytes())
        self.run("duplicate init refuses overwrite", turn, project,
                 failure="already", json_output=True)
        require(before == (config.read_bytes(), (project / "azure.yaml").read_bytes()),
                "Duplicate init changed authored configuration")

        conversation_base = (
            ["ai", "eval", "init", "--judge-model", "ci-judge"]
            if self.pin["conversationModes"] else base
        )
        info = self.run("author conversation eval JSON", conversation_base + [
            "--name", "ci-conversation", "--source", "dataset", "--dataset", str(conversation),
            "--evaluation-level", "conversation", "--evaluator", "builtin.task_completion",
            "--output", "json",
        ], project, json_output=True)
        if self.pin["conversationModes"]:
            require(info["conversationMode"] == "static" and info["simulation"] is None
                    and info["target"] == "", "Unattended conversations must default to static")
        self.run("author trace eval JSON", base + [
            "--name", "ci-trace", "--source", "traces", "--trace-days", "7",
            "--max-traces", "2", "--evaluator", "builtin.task_adherence", "--output", "json",
        ], project, json_output=True)
        final_yaml = config.read_text(encoding="utf-8")
        for expected in ("ci-turn", "ci-conversation", "ci-trace",
                         "evaluation_level: conversation", "max_traces: 2", "lookback_hours: 168"):
            require(expected in final_yaml, f"Authored configuration is missing {expected}")

        invalid_init = [
            ("invalid source", ["--source", "invalid"], "source"),
            ("traces with dataset", ["--source", "traces", "--dataset", str(data)], "dataset"),
            ("zero trace limit", ["--source", "traces", "--max-traces", "0"], "max-traces"),
            ("ignored trace flag", ["--source", "dataset", "--dataset", str(data),
                                    "--max-traces", "2"], "max-traces"),
            ("invalid evaluation level", ["--source", "dataset", "--dataset", str(data),
                                          "--evaluation-level", "invalid"], "evaluation.level"),
            ("unknown init flag", ["--not-a-real-flag"], "unknown flag"),
        ]
        before = (config.read_bytes(), (project / "azure.yaml").read_bytes())
        for name, flags, error in invalid_init:
            self.run(name, base + flags + ["--output", "json"], project,
                     failure=error, json_output=True)
            require(before == (config.read_bytes(), (project / "azure.yaml").read_bytes()),
                    f"{name} mutated authored configuration")

        invalid_dataset = [
            ("missing file flag", ["create", "ci-data"], "from-file"),
            ("invalid dataset name", ["create", "bad name"], "name"),
            ("missing dataset file", ["create", "ci-data", "--from-file", "absent.jsonl"],
             "absent.jsonl"),
            ("malformed dataset row", ["create", "ci-data", "--from-file", str(malformed)], "line 2"),
            ("empty dataset", ["update", "ci-data", "--from-file", str(empty)], "empty"),
            ("missing dataset argument", ["show"], "arg"),
            ("unknown dataset flag", ["list", "--not-a-real-flag"], "unknown flag"),
        ]
        for name, args, error in invalid_dataset:
            self.run(name, ["ai", "dataset", *args, "--output", "json"], project,
                     failure=error, json_output=True)

        invalid_generate = [
            ("dataset-only flag on evaluator generation",
             ["--evaluator", "--evaluation-level", "conversation"], "evaluation-level"),
            ("no-wait generation with output directory",
             ["--no-wait", "--output-dir", "unused-output"], "output-dir"),
            ("no-wait generation with force", ["--no-wait", "--force"], "force"),
            ("negative generation trace window", ["--trace-days", "-1"], "trace-days"),
            ("invalid generation source", ["--dataset", "--from", "invalid"], "invalid"),
            ("negative generation sample cap", ["--dataset", "--max-samples", "-1"], "sample"),
        ]
        for name, args, error in invalid_generate:
            self.run(name, ["ai", "eval", "generate", *args, "--output", "json"], project,
                     failure=error, json_output=True)
            require(before == (config.read_bytes(), (project / "azure.yaml").read_bytes()),
                    f"{name} mutated authored configuration")

        self.output.joinpath("authored-azure.eval.yaml").write_text(
            sanitize(final_yaml, self.root), encoding="utf-8")
        self.output.joinpath("authored-azure.yaml").write_text(
            sanitize((project / "azure.yaml").read_text(encoding="utf-8"), self.root),
            encoding="utf-8",
        )
        if self.pin["conversationModes"]:
            self.exercise_conversation_modes(project_definition)
            self.exercise_unattended_model_inputs(project_definition)
        if self.pin.get("initSeedValidation", False):
            self.exercise_init_seed_validation(project_definition)

    def exercise_init_seed_validation(self, project_definition):
        valid_row = {"test_case_description": "Ask for help finding an order."}
        cases = [
            ("blank description", [{**valid_row, "test_case_description": ""}], "empty or non-text"),
            ("whitespace description", [{"test_case_description": " \t\r\n "}], "empty or non-text"),
            ("missing description", [{"desired_num_turns": 1}], 'no "test_case_description"'),
            ("null description", [{"test_case_description": None}], "empty or non-text"),
            ("numeric description", [{"test_case_description": 42}], "empty or non-text"),
            ("boolean description", [{"test_case_description": True}], "empty or non-text"),
            ("zero desired turns", [{**valid_row, "desired_num_turns": 0}], "positive whole number"),
            ("negative desired turns", [{**valid_row, "desired_num_turns": -1}], "positive whole number"),
            ("fractional desired turns", [{**valid_row, "desired_num_turns": 1.5}], "positive whole number"),
            ("string desired turns", [{**valid_row, "desired_num_turns": "1"}], "positive whole number"),
            ("null desired turns", [{**valid_row, "desired_num_turns": None}], "positive whole number"),
            ("boolean desired turns", [{**valid_row, "desired_num_turns": True}], "positive whole number"),
            ("desired turns exceed explicit cap", [{**valid_row, "desired_num_turns": 21}], "max_turns is 20"),
            ("seed with messages", [{**valid_row, "messages": []}], 'carries "messages"'),
            ("seed with null messages", [{**valid_row, "messages": None}], 'carries "messages"'),
            ("seed with query", [{**valid_row, "query": "Hello"}], 'carries "query"'),
            ("seed with empty query", [{**valid_row, "query": ""}], 'carries "query"'),
            ("seed with null query", [{**valid_row, "query": None}], 'carries "query"'),
            ("seed with response", [{**valid_row, "response": "Hello"}], 'carries "response"'),
            ("seed with empty response", [{**valid_row, "response": ""}], 'carries "response"'),
            ("seed with null response", [{**valid_row, "response": None}], 'carries "response"'),
            ("late invalid seed", [valid_row] * 20 + [{"test_case_description": ""}], "row 21"),
            ("mixed late completed row", [valid_row, {"messages": []}], 'row 2 carries "messages"'),
            ("mixed late turn row", [valid_row, {"query": "Hello"}], 'row 2 carries "query"'),
        ]
        global_config = self.root / "config" / "config.json"
        require(global_config.is_file(), "Installed extensions must have an isolated azd configuration")
        evidence = []

        def refuse_without_writes(label, args, project, error):
            before = snapshot_tree(project)
            private_before = global_config.read_bytes()
            info = self.run(label, args, project, failure=error, json_output=True)
            require(set(info) == {"error"}, f"{label} must emit only one error document")
            after = snapshot_tree(project)
            require(after == before, f"{label} changed authored files, private state or directory layout")
            require(global_config.read_bytes() == private_before,
                    f"{label} changed the isolated azd configuration")
            evidence.append({
                "case": label, "singleJSONError": True,
                "projectUnchanged": True, "privateConfigurationUnchanged": True,
                "projectDigestBefore": sha256(json.dumps(before, sort_keys=True).encode()),
                "projectDigestAfter": sha256(json.dumps(after, sort_keys=True).encode()),
            })

        for layout in ("fresh", "existing"):
            project = self.root / f"seed-refusal-{layout}"
            project.mkdir()
            root_config = project / "azure.yaml"
            root_config.write_text(project_definition, encoding="utf-8")
            private = project / ".azure" / "dev"
            private.mkdir(parents=True)
            private.joinpath(".env").write_text("KEEP=unchanged\n", encoding="utf-8")
            write_json(private / "config.json", {"sentinel": "unchanged"})
            private.joinpath("eval.state").write_text("owned-test-state\n", encoding="utf-8")
            if layout == "existing":
                eval_dir = project / "evals"
                eval_dir.mkdir()
                eval_dir.joinpath("azure.eval.yaml").write_text(
                    "# Preserve authored content and unknown fields\n"
                    "future_metadata: keep\ndatasets: []\nevaluators: []\nevals: []\n",
                    encoding="utf-8",
                )
            rows_path = project / "seed.jsonl"
            args = [
                "ai", "eval", "init", "--name", "ci-seed", "--conversation-mode", "simulation",
                "--target", "ci-agent", "--simulation-model", "ci-simulator", "--judge-model", "ci-judge",
                "--evaluator", "builtin.task_completion", "--max-turns", "20",
                "--dataset", str(rows_path), "--output", "json",
            ]
            for name, rows, error in cases:
                rows_path.write_text(
                    "\n" + "\n\n".join(json.dumps(row) for row in rows) + "\n", encoding="utf-8")
                label = f"init seed {layout}: {name}"
                refuse_without_writes(label, args, project, error)

        for layout in ("declared-file", "nested-ref"):
            project = self.root / f"seed-refusal-{layout}"
            project.mkdir()
            project.joinpath("azure.yaml").write_text(project_definition, encoding="utf-8")
            eval_dir = project / "nested" / "quality"
            parts = eval_dir / "parts"
            inner = parts / "inner"
            inner.mkdir(parents=True)
            parts.joinpath("rows.jsonl").write_text('{"test_case_description":""}\n', encoding="utf-8")
            parts.joinpath("dataset.yaml").write_text("$ref: ./inner/dataset.yaml\n", encoding="utf-8")
            inner.joinpath("dataset.yaml").write_text(
                "file: ../rows.jsonl\nfuture_metadata: keep\n", encoding="utf-8")
            declaration = (
                "    file: ./parts/rows.jsonl\n" if layout == "declared-file"
                else "    $ref: ./parts/dataset.yaml\n"
            )
            eval_dir.joinpath("azure.eval.yaml").write_text(
                "# Preserve selected and unrelated declarations\nfuture_metadata: keep\n"
                "datasets:\n  - name: seeds\n" + declaration
                + "  - name: unrelated\n    $ref: ./absent.yaml\n",
                encoding="utf-8",
            )
            private = project / ".azure" / "dev"
            private.mkdir(parents=True)
            write_json(private / "config.json", {"sentinel": "unchanged"})
            refuse_without_writes(f"init seed validates {layout} without rewriting references", [
                "ai", "eval", "init", "--name", "ci-seed", "--conversation-mode", "simulation",
                "--target", "ci-agent", "--simulation-model", "ci-simulator", "--judge-model", "ci-judge",
                "--evaluator", "builtin.task_completion", "--path", str(eval_dir), "--dataset", "seeds",
                "--output", "json",
            ], project, "empty or non-text")

        for name, desired, max_turns in (
            ("omitted", None, None), ("minimum", 1, 1), ("maximum", 20, 20),
            ("no invented ceiling", 21, None),
        ):
            project = self.root / ("seed-valid-" + name.replace(" ", "-"))
            project.mkdir()
            project.joinpath("azure.yaml").write_text(project_definition, encoding="utf-8")
            rows_path = project / "seed.jsonl"
            row = dict(valid_row)
            if desired is not None:
                row["desired_num_turns"] = desired
            rows_path.write_text(json.dumps(row) + "\n", encoding="utf-8")
            flags = [] if max_turns is None else ["--max-turns", str(max_turns)]
            info = self.run(f"init valid seed {name}", [
                "ai", "eval", "init", "--name", "ci-valid-seed", "--conversation-mode", "simulation",
                "--target", "ci-agent", "--simulation-model", "ci-simulator", "--judge-model", "ci-judge",
                "--evaluator", "builtin.task_completion", "--dataset", str(rows_path), *flags,
                "--output", "json",
            ], project, json_output=True)
            require(info["simulation"].get("max_turns") == max_turns,
                    f"Valid seed {name} changed the caller's optional turn limit")
            config = project / "evals" / "azure.eval.yaml"
            authored = config.read_text(encoding="utf-8")
            require(("max_turns:" in authored) == (max_turns is not None),
                    f"Valid seed {name} wrote an unintended turn limit")
            self.output.joinpath(f"authored-seed-{name.replace(' ', '-')}.yaml").write_text(
                sanitize(authored, self.root), encoding="utf-8")
        for mode, row, flags in (
            ("static", {"messages": [{"role": "user", "content": "Hello"},
                                     {"role": "assistant", "content": "Hello!"}]},
             ["--conversation-mode", "static"]),
            ("turn", {"query": "Hello", "response": "Hello!"},
             ["--source", "dataset", "--evaluation-level", "turn", "--target", "ci-agent"]),
        ):
            project = self.root / f"seed-control-{mode}"
            project.mkdir()
            project.joinpath("azure.yaml").write_text(project_definition, encoding="utf-8")
            rows_path = project / "data.jsonl"
            rows_path.write_text(json.dumps(row) + "\n", encoding="utf-8")
            info = self.run(f"seed validation leaves {mode} mode unchanged", [
                "ai", "eval", "init", "--name", f"ci-{mode}", "--dataset", str(rows_path),
                "--judge-model", "ci-judge", "--evaluator", "builtin.task_completion",
                *flags, "--output", "json",
            ], project, json_output=True)
            require(info["simulation"] is None, f"Non-simulation {mode} control created a simulator")
            require(info["evaluationLevel"] == ("conversation" if mode == "static" else "turn"),
                    f"{mode} control changed evaluation level")
            config = project / "evals" / "azure.eval.yaml"
            require(config.is_file(), f"{mode} control did not author configuration")
        write_json(self.output / "seed-validation.json", evidence)

    def exercise_unattended_model_inputs(self, project_definition):
        project = self.root / "unattended-handoff-inputs"
        project.mkdir()
        root_config = project / "azure.yaml"
        root_config.write_text(project_definition, encoding="utf-8")
        common = [
            "ai", "eval", "init", "--name", "ci-handoff", "--target", "ci-agent",
            "--dataset", "handoff-seeds", "--evaluator", "builtin.task_completion",
        ]
        # Generation itself needs Azure. Exercise only the documented local init
        # inputs, without pretending this fixture came from a generation service.
        for name, flags, required_flags in (
            ("unattended simulation requires both models", ["--conversation-mode", "simulation"],
             ("--simulation-model", "--judge-model")),
            ("unattended simulation does not use simulator as judge",
             ["--conversation-mode", "simulation", "--simulation-model", "ci-simulator"],
             ("--judge-model",)),
            ("unattended turn requires judge", ["--source", "dataset"], ("--judge-model",)),
        ):
            info = self.run(name, common + flags + ["--output", "json"], project,
                            failure="judge-model", json_output=True)
            for flag in required_flags:
                require(flag in info["error"]["message"],
                        f"{name} must identify the unresolved {flag} input")
            require(root_config.read_text(encoding="utf-8") == project_definition
                    and not (project / "evals").exists(),
                    f"{name} wrote a partial scaffold")

        text = self.run("unattended human init consumes explicit models", common + [
            "--conversation-mode", "simulation", "--simulation-model", "ci-simulator",
            "--judge-model", "ci-judge",
        ], project)
        require(re.search(r"Next:\s+azd ai eval create", text),
                "Human init must identify create as the next step, not run it")
        config = project / "evals" / "azure.eval.yaml"
        authored = config.read_text(encoding="utf-8")
        for expected in ("name: ci-handoff", "evaluation_level: conversation", "simulation:",
                         "model: ci-simulator", "model: ci-judge", "name: ci-agent"):
            require(expected in authored, f"Explicit model authoring lost {expected}")
        require("max_turns:" not in authored, "Unspecified turn limit must remain unspecified")
        self.output.joinpath("authored-handoff-inputs.yaml").write_text(
            sanitize(authored, self.root), encoding="utf-8")

    def exercise_conversation_modes(self, project_definition):
        for name, flags, expected in (
            ("default", [], {"model": "ci-simulator", "num_conversations": 1}),
            ("minimum", ["--num-conversations", "1", "--max-turns", "1"],
             {"model": "ci-simulator", "num_conversations": 1, "max_turns": 1}),
            ("maximum", ["--num-conversations", "5", "--max-turns", "20"],
             {"model": "ci-simulator", "num_conversations": 5, "max_turns": 20}),
        ):
            project = self.root / f"simulation-{name}"
            project.mkdir()
            (project / "azure.yaml").write_text(project_definition, encoding="utf-8")
            seeds = project / "seeds.jsonl"
            seeds.write_text('{"test_case_description":"Ask for help finding an order."}\n',
                             encoding="utf-8")
            info = self.run(f"author simulation {name}", [
                "ai", "eval", "init", "--name", f"ci-simulation-{name}",
                "--conversation-mode", "simulation", "--target", "ci-agent",
                "--dataset", str(seeds), "--simulation-model", "ci-simulator",
                "--judge-model", "ci-judge", "--evaluator", "builtin.task_completion",
                *flags, "--output", "json",
            ], project, json_output=True)
            require(
                info["conversationMode"] == "simulation"
                and info["evaluationLevel"] == "conversation" and info["source"] == "dataset"
                and info["simulation"] == expected and info["judgeModel"] == "ci-judge"
                and info["target"] == "ci-agent",
                f"Simulation {name} did not preserve independent models and numeric bounds",
            )
            config = project / "evals" / "azure.eval.yaml"
            text = config.read_text(encoding="utf-8")
            for expected_text in ("simulation:", "target:", "name: ci-agent",
                                  "model: ci-simulator", "model: ci-judge"):
                require(expected_text in text, f"Simulation config missing {expected_text}")
            if name == "default":
                require("max_turns:" not in text, "Omitted max-turns must preserve the service default")
            else:
                require(f"max_turns: {expected['max_turns']}" in text, "Wrong authored turn limit")
            self.output.joinpath(f"authored-simulation-{name}.yaml").write_text(
                sanitize(text, self.root), encoding="utf-8")

        project = self.root / "static-conversation"
        project.mkdir()
        (project / "azure.yaml").write_text(project_definition, encoding="utf-8")
        transcript = project / "transcript.jsonl"
        transcript.write_text(
            '{"messages":[{"role":"user","content":"Hello"},'
            '{"role":"assistant","content":"Hello!"}]}\n', encoding="utf-8")
        info = self.run("author explicit static conversation", [
            "ai", "eval", "init", "--name", "ci-static", "--conversation-mode", "static",
            "--dataset", str(transcript), "--judge-model", "ci-judge",
            "--evaluator", "builtin.task_completion", "--output", "json",
        ], project, json_output=True)
        require(info["target"] == "" and info["simulation"] is None
                and info["conversationMode"] == "static"
                and info["evaluationLevel"] == "conversation", "Static mode selected an agent")
        config = project / "evals" / "azure.eval.yaml"
        text = config.read_text(encoding="utf-8")
        require("target:" not in text and "simulation:" not in text,
                "Static scoring must write neither target nor simulation")
        self.output.joinpath("authored-static.yaml").write_text(
            sanitize(text, self.root), encoding="utf-8")
        before = (config.read_bytes(), (project / "azure.yaml").read_bytes())
        invalid = [
            ("unknown conversation mode", ["--conversation-mode", "invalid"], "static.*simulation"),
            ("static target", ["--conversation-mode", "static", "--target", "ci-agent"], "target"),
            ("static simulator", ["--conversation-mode", "static", "--simulation-model", "ci-sim"],
             "simulation-model"),
            ("static count", ["--conversation-mode", "static", "--num-conversations", "1"],
             "num-conversations"),
            ("static turns", ["--conversation-mode", "static", "--max-turns", "1"], "max-turns"),
            ("simulation trace source", ["--conversation-mode", "simulation", "--source", "traces"],
             "conversation-mode"),
            ("simulation turn level", ["--conversation-mode", "simulation", "--evaluation-level", "turn"],
             "conversation-mode"),
            ("simulation count zero", ["--conversation-mode", "simulation", "--num-conversations", "0"],
             "num-conversations"),
            ("simulation count over maximum",
             ["--conversation-mode", "simulation", "--num-conversations", "6"], "num-conversations"),
            ("simulation turns zero", ["--conversation-mode", "simulation", "--max-turns", "0"],
             "max-turns"),
            ("simulation turns over maximum", ["--conversation-mode", "simulation", "--max-turns", "21"],
             "max-turns"),
            ("missing independent simulation model", [
                "--conversation-mode", "simulation", "--target", "ci-agent", "--dataset", "seeds",
                "--judge-model", "ci-judge",
            ], "simulation-model"),
        ]
        for name, flags, error in invalid:
            self.run(name, ["ai", "eval", "init", *flags, "--output", "json"], project,
                     failure=error, json_output=True)
            require(before == (config.read_bytes(), (project / "azure.yaml").read_bytes()),
                    f"{name} changed existing authored configuration")

        for field in ("num_conversations", "max_turns"):
            for style in ("block", "flow"):
                invalid_config = self.root / f"invalid-{field}-{style}.yaml"
                if style == "flow":
                    write_json(invalid_config, {
                        "evals": [{"name": "ci-invalid", "simulation": {
                            "model": "ci-simulator", field: 0,
                        }}],
                    })
                else:
                    invalid_config.write_text(
                        "evals:\n  - name: ci-invalid\n    simulation:\n"
                        f"      model: ci-simulator\n      {field}: 0\n", encoding="utf-8",
                    )
                self.run(f"production loader rejects zero {field} {style}", [
                    "ai", "eval", "create", "--from-file", str(invalid_config), "--output", "json",
                ], failure=rf"simulation\.{field} is 0", json_output=True)
        invalid_config = self.root / "invalid-simulation-typo.yaml"
        invalid_config.write_text(
            "evals:\n  - name: ci-invalid\n    simulation:\n"
            "      model: ci-simulator\n      max_turn: 2\n", encoding="utf-8")
        self.run("production loader rejects unknown simulation key", [
            "ai", "eval", "create", "--from-file", str(invalid_config), "--output", "json",
        ], failure='unknown key "max_turn"', json_output=True)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--output", required=True, type=Path)
    args = parser.parse_args()
    args.output.mkdir(parents=True, exist_ok=False)
    pin = json.loads(Path(__file__).with_name("candidate.json").read_text(encoding="utf-8"))
    write_json(args.output / "candidate.json", pin)
    with tempfile.TemporaryDirectory(prefix="eval-cli-proof-") as directory:
        proof = Proof(Path(directory), args.output, pin)
        report = {
            "coverage": "offline CLI only",
            "liveCloudEvaluation": "NOT RUN",
            "cloudQualityGate": "NOT RUN",
            "interactiveCorrection": "NOT RUN",
            "failedCloudRunVerification": "NOT RUN",
            "initSeedValidationEnabled": pin.get("initSeedValidation", False),
            "authRequired": (
                "An existing Azure service identity with an authorized GitHub OIDC trust "
                "for this fork/ref, tenant/client identifiers, and least-privilege access "
                "to an isolated Foundry project and its existing model/agent resources. "
                "Devbox user credentials must not be copied into CI."
            ),
            "platform": proof.platform,
            "workflowCommit": os.environ.get("GITHUB_SHA"),
            "runUrl": (
                f"https://github.com/{os.environ['GITHUB_REPOSITORY']}/actions/runs/"
                f"{os.environ['GITHUB_RUN_ID']}"
            ) if "GITHUB_RUN_ID" in os.environ else None,
            "status": "failed",
        }
        try:
            proof.install()
            proof.exercise()
            report["status"] = "passed"
        finally:
            report["checks"] = proof.checks
            write_json(args.output / "results.json", report)
            write_json(args.output / "commands.json", proof.commands)
            summary = (
                f"## Offline evaluation CLI: {report['status']}\n\n"
                f"- Release: `{pin['releaseTag']}`\n"
                f"- Source: `{pin['sourceCommit'] or 'unattested baseline'}`\n"
                f"- Platform: `{proof.platform}`\n"
                f"- CLI command checks passed: {len(proof.checks)}\n"
                "- Live cloud evaluation and quality gate: **NOT RUN** (no CI identity).\n"
                "- Evidence contains only synthetic authoring, sanitized command output, "
                "versions and checksum pins. No auth/config caches are uploaded.\n"
            )
            args.output.joinpath("summary.md").write_text(summary, encoding="utf-8")
            if "GITHUB_STEP_SUMMARY" in os.environ:
                with open(os.environ["GITHUB_STEP_SUMMARY"], "a", encoding="utf-8") as stream:
                    stream.write(summary)


if __name__ == "__main__":
    main()
