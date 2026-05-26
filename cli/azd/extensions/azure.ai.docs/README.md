# Foundry docs for AI agents (Preview)

Single front door for agent-friendly documentation across every
`azure.ai.*` extension. The markdown is embedded in this extension --
install once, and `azd ai doc <category> <topic>` returns documentation
for any covered ai.* extension without requiring the sibling extension
to be installed.

The shape mirrors a familiar `skills` surface:

```bash
# Top-level index -- which ai.* extensions have docs
azd ai doc

# List topics for the agents extension
azd ai doc agent

# Print one topic's markdown
azd ai doc agent initialize
azd ai doc agent configure
azd ai doc agent investigate
azd ai doc agent operate
```

Each topic is a contract an agent reads to drive the matching CLI
commands: exact invocations, JSON shape examples, error codes,
confirmation-envelope handling.

## Local development

The first install in a new environment needs the full pack + publish +
install flow because `azd x build` alone only deploys the binary to
`~/.azd/extensions/<id>/` -- not the `extension.yaml` manifest. Without
the manifest azd can't register the command surface, so `azd ai doc`
will not appear under `azd ai`.

```bash
cd cli/azd/extensions/azure.ai.docs

# First time only
azd x build
azd x pack
azd x publish
azd ext install azure.ai.docs

# After that, iterate with watch (rebuilds + redeploys binary)
azd x watch
```

## Adding topics for another ai.* extension

The repo layout is intentionally simple:

```
internal/cmd/
  skills/
    agent/            <-- topics for azure.ai.agents
      initialize.md
      configure.md
      investigate.md
      operate.md
    toolbox/          <-- future: topics for azure.ai.toolboxes
      ...
    project/          <-- future: topics for azure.ai.projects
      ...
  doc_index.go        <-- docCategories table (one entry per skills/ subdir)
  doc_agent.go        <-- per-extension subcommand
```

To add a new sibling:

1. Drop `skills/<sibling>/<topic>.md` files into this extension.
2. Add an entry to `docCategories` in `doc_index.go`.
3. Add a `new<Sibling>Command()` constructor mirroring `newAgentCommand()`
   and register it in `root.go`.

No coordination with the sibling extension is required; this extension is
the source of truth for its agent-friendly docs.
