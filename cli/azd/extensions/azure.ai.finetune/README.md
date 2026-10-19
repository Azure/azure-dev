# `azd` Finetune Extension

An azd Finetune extension

Global options are managed by the azd SDK, including `--debug`, `--no-prompt`,
`-e/--environment`, and `-o/--output`. Init continues to accept the legacy `-n`
environment shorthand; conflicting environment selectors are rejected before authentication.
Job list output supports `table` and `json`; job show also supports `yaml`.
Both commands default to `table` and reject unsupported formats before contacting Azure.
