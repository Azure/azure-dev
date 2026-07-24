applyTo:
  - "cli/azd/extensions/azure.ai.*/**"
---
# Azure AI (`azure.ai.*`) extensions - public documentation backstop

- When reviewing a change under `cli/azd/extensions/azure.ai.*`, check whether it introduces user-visible
  surface that Microsoft Learn should cover: a new environment variable (for example a routines HTTP timeout),
  a new command, subcommand, or flag, a changed default or behavior a user would notice, or a new extension
  capability.

- If it does and the PR adds no matching documentation, call it out and note that it should be tracked for
  public docs - the appropriate home is an issue labeled `area/public-docs` plus the matching `ext-*` label
  (for example `ext-routines` for `azure.ai.routines`).

- This is a backstop only. The `Foundry Docs Tracker` workflow
  (`.github/workflows/foundry-docs-tracker.md`) opens and maintains that tracking issue automatically across the
  PR's lifecycle, so do not open a second issue by hand - just make sure the doc-worthy change is visible in
  review.
