---
short: Wire downloaded SKILL.md files into a Hosted agent so the runtime injects them as instructions.
order: 40
---
# Skill consume: wiring a skill into a Hosted agent

Skills only matter at runtime when a Hosted agent **bundles** the downloaded SKILL.md files into its container image and the agent runtime injects their contents as additional instructions on every session. This topic covers the wiring on the agent side.

For the CLI that pulls files down, see `share`. For mental model, see `overview`.

## Support matrix

| Agent type    | Skills support |
| ------------- | -------------- |
| Hosted agent  | Yes            |
| Prompt agent  | No (not supported today) |

Skills are a Hosted-agent-only feature in the current preview. The Prompt agent path does not surface a hook for injecting downloaded skill content. If your agent is a Prompt agent, embed the guidance directly in the prompt or migrate to a Hosted agent.

## Bundle layout inside the agent project

Each downloaded skill lives in its **own subdirectory** under `skills/` in the agent project tree. The Foundry sample convention is:

```
<agent-project>/
  main.py                  -- agent code that loads skills at session start
  agent.yaml
  agent.manifest.yaml
  requirements.txt
  skills/
    greet-user/
      SKILL.md             -- one SKILL.md per skill, under its own subdir
    joke/
      SKILL.md
```

The bundled `SKILL.md` is at `skills/<name>/SKILL.md`, NOT at `skills/SKILL.md` and NOT at the project root. The agent runtime walks `skills/` and treats each subdirectory with a `SKILL.md` as one skill.

This is the opposite of the `create --file ./skill.zip` upload convention, where `SKILL.md` lives at the ARCHIVE root. The CLI's default `azd ai skill download <name>` writes to `.agents/skills/<name>/` -- you copy or `--output-dir` into `skills/<name>/` inside the agent project tree before deploying.

## Session-time instruction injection

The agent's code passes a `skill_directories` parameter (or its equivalent in the SDK you're using) at session creation. The SDK walks each named directory, reads every `SKILL.md`, and prepends the bodies (without front-matter) to the model's instructions for that session.

The exact symbol depends on the SDK. The GitHub Copilot SDK sample referenced in the Foundry docs uses `CopilotClient(skill_directories=["./skills"])`. Other SDKs may surface this as a per-session option, a constructor argument, or an environment-variable convention -- check the SDK README for the agent runtime you scaffolded.

## End-to-end recipe: download + deploy

Based on the GitHub Copilot sample from the Foundry skills docs.

```bash
# 1) Scaffold the agent project from the manifest.
azd ai agent init -m https://github.com/microsoft-foundry/foundry-samples/blob/main/samples/python/hosted-agents/bring-your-own/invocations/github-copilot/agent.manifest.yaml

# 2) Set the GitHub fine-grained PAT (Copilot Requests -> Read-only).
#    Classic ghp_* tokens are not supported -- use github_pat_*.
azd env set GITHUB_TOKEN="github_pat_..."

# 3) Download the skills you want this agent to honor. Pipe each one
#    into the agent's skills/<name>/ directory.
azd ai skill download greet-user --output-dir ./skills/greet-user

# 4) Run locally to verify.
azd ai agent run
# In a separate terminal:
azd ai agent invoke --local '{"input": "Hi, my name is Alex!"}'

# 5) Deploy to Foundry.
azd provision
azd deploy
azd ai agent invoke '{"input": "Hi, my name is Alex!"}'
```

PowerShell note: when invoking with an inline JSON string, escape the inner quotes -- `azd ai agent invoke --local '{\"input\": \"Hi, my name is Alex!\"}'`.

## Updating a skill on a deployed agent

`azd deploy` does NOT refresh skill content from Foundry on its own. The runtime reads the SKILL.md files that were baked into the container image at build time. To pick up a new skill version:

```bash
# 1) Pull the new default version into the agent project tree.
azd ai skill download greet-user --output-dir ./skills/greet-user --force

# 2) Rebuild and redeploy.
azd deploy
```

`--force` is required because the destination directory already contains the previous SKILL.md. The safe-extract conflict check refuses to clobber existing files without it.

If you have many skills and want to refresh everything in one command, script it:

```bash
azd ai skill list --output json | jq -r '.[].name' | while read name; do
  azd ai skill download "$name" --output-dir "./skills/$name" --force
done
azd deploy
```

## Pinning a specific version into the bundle

By default `azd ai skill download <name>` pulls `default_version`. To pin a deployed agent to a specific version, use `--version`:

```bash
azd ai skill download greet-user --version v3 --output-dir ./skills/greet-user --force
azd deploy
```

The agent will continue to use `v3` even if a teammate later runs `azd ai skill update greet-user` to promote a newer version, until you re-download.

## Removing a skill from an agent

Delete the corresponding `skills/<name>/` subdirectory in the agent project tree and redeploy. The skill stays on Foundry (other agents can still consume it); only this agent forgets it.

```bash
rm -rf ./skills/greet-user
azd deploy
```

To remove the skill from Foundry entirely (every consuming agent will fail to download it on the next refresh), use `azd ai skill delete greet-user --force` -- see `manage`.

## Front-matter and the runtime

The Hosted agent runtime strips YAML front-matter before injecting the SKILL.md body. The body is what reaches the model; `name:` and `description:` are metadata for the Foundry skill catalog and Hosted agent listings, not for the prompt. Keep behavioral guidance in the Markdown body.

## Source control

Treat the agent's `skills/` tree as **build input** managed by your release process. Two patterns work:

* **Pull at build time** -- a CI step runs `azd ai skill download` for each declared skill before `azd deploy`. The repo does not contain SKILL.md files; the source of truth is Foundry. Fast iteration but requires CI auth to the Foundry project.
* **Commit downloaded files** -- run `azd ai skill download` locally, commit the resulting `skills/<name>/SKILL.md` files. The repo is reproducible without Foundry credentials but drifts from Foundry until someone re-runs `download`.

Pick one per project. Mixing them invites silent drift.
