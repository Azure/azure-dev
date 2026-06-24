# Copyright (c) Microsoft Corporation. All rights reserved.
# Licensed under the MIT License.

"""strip_suffix.py removes stale, file-derived suffixes from declarations left
over from catch-all coverage files (for example ``TestFoo_Final`` -> ``TestFoo``
or the test-helper ``fakeLocator_r10`` -> ``fakeLocator``) as part of issue
#8799.

It targets two kinds of file-derived suffix that the catch-all files used to keep
identically-purposed declarations from colliding before they were redistributed:

* word suffixes -- ``_Final``, ``_Finish``, ``_Push``, ``_Deeper``, ``_Extra``,
  ``_Additional``, ``_More``, ``_Coverage``/``_Coverage3``, ``_Cov3``, ``_Wave3``;
* round suffixes -- ``_r9``, ``_r10``, ``_round8`` ... (the per-wave numbering
  used while the coverage files were being split apart).

Both ``Test``/``Benchmark`` functions and the unexported test helpers they share
(funcs, methods, types, vars, consts) are rewritten, including every reference,
so the package keeps compiling.

A rename is only applied when it is collision-free within the package: the
stripped target must not already appear as a whole word anywhere in the package,
and no two suffixed declarations may strip to the same target. Anything that
would collide is left untouched (those are genuine duplicate tests/helpers that
need a conscious dedup decision, not a mechanical rename). Run from the directory
you want to rewrite (for example cli/azd).

Usage:
    python3 tools/testreorg/strip_suffix.py
"""

import collections
import glob
import os
import re

# Trailing, file-derived suffixes to strip.
SUFFIX_RE = re.compile(
    r"_(?:"
    r"Coverage[0-9]*|Cover[0-9]+|Cov[0-9]+|"
    r"Final|Finish|Push|Deeper|Extra|Additional|More|"
    r"Wave[0-9]+|wave[0-9]+|"
    r"r[0-9]+|rnd[0-9]+|round[0-9]+|Round[0-9]+"
    r")$"
)

# Top-level declarations and methods (the names that may carry a stale suffix).
DEF_RE = re.compile(
    r"^(?:func (?:\([^)]*\) )?|type |var |const )([A-Za-z_][A-Za-z0-9_]*)", re.M
)
WORD_RE = re.compile(r"[A-Za-z_][A-Za-z0-9_]*")


def main() -> None:
    dirs = collections.defaultdict(list)
    for f in glob.glob("./**/*_test.go", recursive=True):
        if "/extensions/" in f or "/tools/testreorg/" in f:
            continue
        dirs[os.path.dirname(f)].append(f)

    renamed = 0
    for files in dirs.values():
        perfile = {f: open(f).read() for f in files}
        blob = "\n".join(perfile.values())
        words = set(WORD_RE.findall(blob))

        # Collect every declared name in the package that carries a stale suffix.
        stale = set()
        for txt in perfile.values():
            for m in DEF_RE.finditer(txt):
                if SUFFIX_RE.search(m.group(1)):
                    stale.add(m.group(1))

        target_count = collections.Counter(SUFFIX_RE.sub("", n) for n in stale)

        renames = {}
        for name in stale:
            target = SUFFIX_RE.sub("", name)
            if target in words:
                continue  # would collide with an existing declaration/reference
            if target_count[target] != 1:
                continue  # two suffixed declarations strip to the same target
            renames[name] = target

        if not renames:
            continue

        # Apply every rename as a whole-word replacement so definitions, call
        # sites, and doc comments stay in sync.
        pat = re.compile(r"\b(" + "|".join(map(re.escape, renames)) + r")\b")
        for f in files:
            perfile[f] = pat.sub(lambda m: renames[m.group(1)], perfile[f])
            open(f, "w").write(perfile[f])
        renamed += len(renames)

    print("renamed", renamed, "declarations")


if __name__ == "__main__":
    main()
