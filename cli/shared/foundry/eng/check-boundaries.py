#!/usr/bin/env python3
# Copyright (c) Microsoft Corporation. All rights reserved.
# Licensed under the MIT License.

"""Validate the independently buildable Foundry module; never mutate sibling modules."""

import json
import os
from pathlib import Path
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[1]
ENV = {**os.environ, "GOWORK": "off"}


def run(*args):
    return subprocess.check_output(args, cwd=ROOT, env=ENV, text=True).strip()


def forbidden(path):
    # SDK-internal packages are fine; extension-internal packages and core are not.
    return (
        path == "github.com/azure/azure-dev/cli/azd"
        or path.startswith("github.com/azure/azure-dev/cli/azd/")
        or ((path.startswith("azure.ai.") or path.startswith("azureai")) and "/internal/" in path)
    )


def main():
    module = json.loads(run("go", "mod", "edit", "-json"))
    if module["Module"]["Path"] != "github.com/azure/azure-dev/cli/shared/foundry":
        raise ValueError("unexpected shared-module identity")
    if module.get("Replace"):
        raise ValueError("published shared modules must not contain replace directives")
    modules = run("go", "list", "-m", "-f", "{{.Path}}", "all").splitlines()
    imports = run("go", "list", "-deps", "-test", "-f", "{{.ImportPath}}", "./...").splitlines()
    bad = sorted({path for path in modules + imports if forbidden(path)})
    if bad:
        raise ValueError("forbidden dependency: " + ", ".join(bad))
    go_files = sorted(ROOT.glob("access/**/*.go"))
    header = "// Copyright (c) Microsoft Corporation. All rights reserved.\n// Licensed under the MIT License."
    for path in go_files:
        if not path.read_text(encoding="utf-8").startswith(header):
            raise ValueError(f"missing copyright header: {path.relative_to(ROOT)}")
    if run("gofmt", "-l", "access"):
        raise ValueError("run gofmt -w access before committing")
    print("Independent module, dependency boundaries, formatting, and copyright checks passed.")


if __name__ == "__main__":
    try:
        main()
    except (ValueError, subprocess.CalledProcessError) as error:
        print(error, file=sys.stderr)
        sys.exit(1)
