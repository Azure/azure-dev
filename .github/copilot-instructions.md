# Azure Developer CLI (azd) - Instructions for Copilot Chat and Copilot code review

For any work in this repository, especially for code reviews, you MUST read [cli/azd/AGENTS.md](../cli/azd/AGENTS.md) in its entirety (every line) first.

## Required extension contract review

For every PR review, including fork PRs, inspect the complete changed-file list first. If any file under
`cli/azd/extensions/` changed, you MUST read and apply
[extension-user-contract-review](skills/extension-user-contract-review/SKILL.md) before general review lenses.
Do not wait until a contract change is already suspected. This requirement applies even when
`azd-code-reviewer` is not otherwise loaded.

Return one inline comment for each distinct new, high-confidence contract change, preserving existing-thread
deduplication and per-thread approved experiences spec tracking. Before finishing, audit that coverage and comments
survived triage. Include the contract pass's completion status and counts in the review summary or supported result
channel; if required evidence or output capacity is unavailable, explicitly report `blocked`, not a clean review.
