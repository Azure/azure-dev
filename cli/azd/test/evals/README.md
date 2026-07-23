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

| Command                     | Targets                                                                                      |
| --------------------------- | -------------------------------------------------------------------------------------------- |
| `npm run eval:simple`       | `eval.yaml` — starter example showing Vally features (fixtures, worktrees)                   |
| `npm run eval:troubleshoot` | `eval-azd-troubleshooting.yaml` — asking the LLM about azd, but without any files (pure Q&A) |
| `npm run eval:deploy`       | `eval-azd-deploy.yaml` — does the model suggest azd for an app + Azure, skill loaded         |
| `npm run eval:experiment`   | `eval-azd.experiments.yaml` — skills on/off baseline (see the file header)                   |
| `npm run report`            | generates a simple report from latest eval and experiment runs                               |

## Main folders

Vally evaluations are controlled by the `eval.yaml`. The structure for everything
else is up to us:

```text
evals/
├── eval.yaml           # eval definition
├── fixtures/           # input files used by the eval
├── graders/            # custom grader logic
└── vally-results/      # output from local runs
```

## Useful links

- Vally docs: <https://aka.ms/vally>
- Vally samples: <https://github.com/microsoft/vally/tree/main/samples>
