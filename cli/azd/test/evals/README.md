# Evals quick start

This folder contains a small Vally eval example for the Azure Developer CLI repo.

## Run locally

From this folder, install dependencies and run the eval:

```bash
# assuming node v24+
npm ci
npx vally eval -e eval.yaml
```

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

- Vally docs: https://aka.ms/vally
- Vally samples: https://github.com/microsoft/vally/tree/main/samples
