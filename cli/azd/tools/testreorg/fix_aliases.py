# Copyright (c) Microsoft Corporation. All rights reserved.
# Licensed under the MIT License.

"""fix_aliases.py restores aliased imports that goimports cannot infer after
test declarations are moved between files (for example
``msalcache "github.com/.../apps/cache"``), as part of issue #8799.

It repeatedly test-compiles the given packages, scans the build output for
``undefined: <alias>`` errors, inserts the matching aliased import into each
offending file, and re-runs goimports/gofmt, until no known-alias errors remain.

The alias map file has one ``alias|import/path`` entry per line. Recover it from
the deleted catch-all files in git history, for example:

    git show <base>:<deleted_file> | grep -oE '\\t[a-z][A-Za-z0-9_]* "[^"]+"'

Usage:
    python3 tools/testreorg/fix_aliases.py <alias_map.txt> <pkg> [<pkg> ...]
"""

import os
import re
import subprocess
import sys

MAX_ITERATIONS = 8


def main(argv: list[str]) -> int:
    if len(argv) < 2:
        print("usage: fix_aliases.py <alias_map.txt> <pkg> [<pkg> ...]", file=sys.stderr)
        return 2

    alias_map_path, pkgs = argv[0], argv[1:]
    goimports = subprocess.check_output(["go", "env", "GOPATH"]).decode().strip() + "/bin/goimports"

    amap = {}
    for line in open(alias_map_path):
        line = line.strip()
        if not line:
            continue
        alias, path = line.split("|", 1)
        amap[alias] = path

    for it in range(MAX_ITERATIONS):
        out = subprocess.run(
            ["go", "test", "-vet=off", "-run", "^$"] + pkgs,
            capture_output=True,
            text=True,
        )
        errs = out.stdout + out.stderr
        pairs = set(re.findall(r'(\S+\.go):\d+:\d+: undefined: (\w+)', errs))
        todo = [(f, a) for f, a in pairs if a in amap]
        if not todo:
            print("no more alias undefineds at iter", it)
            return 0

        changed = set()
        for f, a in todo:
            if not os.path.exists(f):
                continue
            txt = open(f).read()
            imp = f'\t{a} "{amap[a]}"\n'
            if imp in txt:
                continue
            # Insert after the first "import (" block, else add a new block.
            nt = re.sub(r'import \(\n', 'import (\n' + imp, txt, count=1)
            if nt == txt:
                nt = re.sub(r'(package \w+\n)', r'\1\nimport (\n' + imp + ')\n', txt, count=1)
            open(f, "w").write(nt)
            changed.add(f)

        for f in changed:
            subprocess.run([goimports, "-w", "-local", "github.com/azure/azure-dev", f])
            subprocess.run(["gofmt", "-s", "-w", f])
        print("iter", it, "fixed", len(changed), "files")

    print("stopped after", MAX_ITERATIONS, "iterations")
    return 1


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
