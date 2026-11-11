---
name: azd-extensions
description: "Find and follow extension-owned workflows when working on code, features, tests, or documentation for the microsoft.foundry extensions: azure.ai.agents, azure.ai.connections, azure.ai.inspector, azure.ai.projects, azure.ai.routines, azure.ai.skills, and azure.ai.toolboxes. Use for feature test plans, unit tests, CLI regression, playback, live integration, E2E scenario authoring, and explicitly requested scenario runs. Read the extension-local index for current workflows rather than relying on a fixed list here."
---

# AZD extension workflows

1. Read the [extension-owned routing index](../../../cli/azd/extensions/skills/README.md).
   Stop and report the missing index if it cannot be read.
2. Select the workflow matching the user's intent and affected extension.
   Read and follow that workflow before acting; resolve its links from its file.
   Stop if a selected workflow is missing. Do not invent a fallback.
3. If no workflow matches, follow the applicable repository and extension
   instructions without claiming a specialized workflow exists. Ask when
   materially different workflows could apply.

Routing does not authorize authentication, resource creation, billable calls, or
scenario execution. Preserve each workflow's consent gates and custom-agent
invocation restrictions. A request to author tests is not a request to run live tests.
