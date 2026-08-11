# Evals quick start

This folder contains a small Vally eval example for the Azure Developer CLI repo.

## Run locally

From this folder, install dependencies and run the eval:

```bash
# assuming node v24+
npm ci

# see below for various run scripts you can use to try out different evals, or try them all
# at once!
```

## Targets

NOTE: the evals are a first pass, so you will see them fail - things have yet to be tuned.

Each eval definition targets a different azd scenario. Run them via npm:

| Command                          | Targets                                                                                      |
| -------------------------------- | -------------------------------------------------------------------------------------------- |
| `npm run eval:simple`            | `eval.yaml` — starter example showing Vally features (fixtures, worktrees)                   |
| `npm run eval:qna`               | `eval-azd-qna.yaml` — asking the LLM about azd, but without any files (pure Q&A)             |
| `npm run eval:deploy`            | `eval-azd-deploy.yaml` — does the model suggest azd for an app + Azure, skill loaded         |
| `npm run eval:experiment`        | `eval-azd.experiments.yaml` — skills on/off baseline (see the file header)                   |
| `npm run eval:foundry:skill`     | `eval-foundry-skill.yaml` — does the official Foundry skill route and comply with its own rules |
| `npm run eval:foundry:authoring` | `eval-foundry-authoring.yaml` — editing Foundry service entries in `azure.yaml`              |
| `npm run eval:foundry:cli`       | `eval-foundry-cli.yaml` — driving the real `azd ai` CLI offline                              |
| `npm run eval:foundry:experiment`| `eval-foundry.experiments.yaml` — Foundry skill on/off × model                               |
| `npm run report`                 | generates a simple report from latest eval and experiment runs                               |

## Main folders

Each Vally evaluation is controlled by its own `eval-*.yaml`. The structure for
everything else is up to us:

```text
evals/
├── eval.yaml                     # starter example (npm run eval:simple)
├── eval-azd-qna.yaml             # Q&A / error scenarios
├── eval-azd-deploy.yaml          # deploy + environment scenarios
├── eval-azd.experiments.yaml     # skills on/off experiment
├── eval-foundry-skill.yaml       # Foundry skill routing + rule compliance
├── eval-foundry-authoring.yaml   # Foundry azure.yaml authoring
├── eval-foundry-cli.yaml         # Foundry CLI, offline
├── eval-foundry.experiments.yaml # Foundry skill on/off experiment
├── fixtures/                     # input files mounted into eval worktrees
├── graders/                      # custom grader logic
├── scripts/                      # setup helpers (fetching external skills)
├── skills/azd/                   # the azd skill injected during evals
├── skills/.external/             # skills fetched from other repos (gitignored)
├── vally_report.go               # report generator (npm run report)
|  # these are output folders from vally itself. They're just JSON/JSONL files, so you can parse
|  # them yourself, or just use vally_report.go as a starting point.
|  #
├── vally-results/                # output from local eval runs
└── vally-experiment-results/     # output from local experiment runs
```

## Foundry evals

These target the Foundry/AI extensions (`microsoft.foundry` and the `azure.ai.*`
extensions under `cli/azd/extensions/`). They are a small seed meant to be built on,
not a comprehensive suite.

| Eval | Needs | What it asks |
| --- | --- | --- |
| `eval-foundry-skill.yaml` | the fetched skill | Does the official skill route to the right sub-skill, and does the agent tag azd commands the way the skill requires? |
| `eval-foundry-authoring.yaml` | nothing | Can an agent migrate a legacy agent definition, and add a second agent across several turns? |
| `eval-foundry-cli.yaml` | `azd` + Foundry extensions | Can an agent discover and drive the Foundry CLI, and stay read-only when asked? |

Two stimuli each -- a pattern to copy rather than a sweep.

They run from their own workflow, `.github/workflows/vally-foundry-ci.yml`, so the core
azd eval gate stays independent of Foundry setup. Non-blocking for now.

Thresholds (`0.7`) sit under where we score today, so we hear about it if things get
worse without this being red every run. The per-grader breakdown in the job summary is
more useful than the pass/fail. One known gap is under the line: `azd ai agent doctor`
doesn't get discovered, since the agent reads `azure.yaml` instead. It still answers
correctly, so that grader is left strict on purpose.

### The skill is fetched, not vendored

`eval-foundry-skill.yaml` evaluates the official `microsoft-foundry` skill from
[microsoft/azure-skills](https://github.com/microsoft/azure-skills/tree/main/skills/microsoft-foundry),
fetched rather than checked in so it doesn't go stale:

```bash
npm run fetch:skill                       # tracks main
node scripts/fetch-foundry-skill.mjs <ref>   # or pin to a branch, tag, or commit
```

It lands in `skills/.external/microsoft-foundry/` (gitignored). The script records the
resolved commit in `.skill-ref`, which CI uploads so you can tell which version a run saw.

### Running the CLI eval

`eval-foundry-cli.yaml` shells out to the real CLI, so it needs the extensions installed.
No Azure sign-in, creates nothing.

```bash
azd ext install microsoft.foundry
npm run eval:foundry:cli
```

Locally it reads your real azd config, since that's where the extensions live. The stimuli
are read-only and destructive commands are in `disallowed` graders, but that's a guardrail,
not a sandbox. CI installs into a job-local dir.

## Useful links

- Vally docs: <https://aka.ms/vally>
- Vally samples: <https://github.com/microsoft/vally/tree/main/samples>
