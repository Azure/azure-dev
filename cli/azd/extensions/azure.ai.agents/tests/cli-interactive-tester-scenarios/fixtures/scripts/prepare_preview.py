# Copyright (c) Microsoft Corporation. All rights reserved.
# Licensed under the MIT License.

"""Seed deterministic differences in the copied Tier 2 scaffold without dependencies."""

import json
import re
import sys
from pathlib import Path


def prepare_preview(path: Path, instance: str) -> None:
    if not re.fullmatch(r"[a-z0-9-]+", instance):
        raise ValueError("invalid preview instance")
    lines = path.read_text(encoding="utf-8").splitlines(keepends=True)
    start = next(i for i, line in enumerate(lines) if line.rstrip() == "services:")
    end = next(
        (i for i in range(start + 1, len(lines)) if re.match(r"^[^\s#]", lines[i])),
        len(lines),
    )
    boundaries = [
        i for i in range(start + 1, end) if re.match(r"^  \S.*:\s*$", lines[i])
    ] + [end]
    hosted = [
        (begin, finish)
        for begin, finish in zip(boundaries, boundaries[1:])
        if any(line.rstrip() == "    kind: hosted" for line in lines[begin:finish])
    ]
    if len(hosted) != 1:
        raise ValueError("expected exactly one inline hosted service in the shared scaffold")
    begin, finish = hosted[0]
    replacement = []
    skipping = False
    for line in lines[begin:finish]:
        if re.match(r"^    (description|metadata|environmentVariables):", line):
            skipping = True
            continue
        if re.match(r"^    \S", line):
            skipping = False
        if not skipping:
            replacement.append(line)
    description = (
        f"Preview visible {instance}; credential ${{AZD_PREVIEW_SCENARIO_SECRET}}; "
        "docs https://preview-user:preview-password@example.com/help"
        "?sig=preview-signature#preview-fragment"
    )
    replacement.extend(
        [
            f"    description: {json.dumps(description)}\n",
            "    metadata:\n",
            f"      tags: {json.dumps(['preview-public', instance])}\n",
            "    environmentVariables:\n",
            "      - name: PREVIEW_PUBLIC\n",
            f"        value: {json.dumps('visible-' + instance)}\n",
            "      - name: PREVIEW_SECRET\n",
            '        value: "${AZD_PREVIEW_SCENARIO_SECRET}"\n',
        ]
    )
    path.write_text("".join(lines[:begin] + replacement + lines[finish:]), encoding="utf-8")


if __name__ == "__main__":
    prepare_preview(Path(sys.argv[1]), sys.argv[2])
    print("Prepared isolated hosted preview differences")
