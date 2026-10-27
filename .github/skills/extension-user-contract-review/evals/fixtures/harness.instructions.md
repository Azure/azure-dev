Review the synthetic PR evidence in the task using the installed azd-code-reviewer skill and its real reference files.
The task supplies all available PR metadata, complete source behavior, changed-line locations, and review history.
Use that evidence instead of making GitHub requests or executing PR code. Do not post anything to GitHub.
The local cli/azd/AGENTS.md is a fixture; installed skill reference files remain authoritative for the review process.

Return the host review object as JSON only, without Markdown fences:
{"body": "<review summary>", "comments": [{"path": "<file>", "line": 42, "severity": "suggestion", "body": "<inline comment>"}]}
Use an empty comments array when appropriate. Do not write a report file.
