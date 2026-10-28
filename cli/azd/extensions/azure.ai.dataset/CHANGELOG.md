# Release History

## 1.0.0-beta.2 (2026-10-12)

### Bugs Fixed

- [[#10151]](https://github.com/Azure/azure-dev/pull/10151) Fix `azd ai dataset download --output-file` rejecting a single uploaded file when its credential is scoped to the parent container.

### Other Changes

- [[#10277]](https://github.com/Azure/azure-dev/pull/10277) Explain in the help how `azd ai dataset` relates to `azd ai eval dataset`, and include the `azd` command prefix in help paths.
- [[#10102]](https://github.com/Azure/azure-dev/pull/10102) Report a `dataset.published` usage event once a dataset version is written, when installed from the official registry; it carries no dataset names, paths or endpoints.

## 1.0.0-beta.1 (2026-09-17)

First release of the Foundry datasets extension.

### Features Added

- Register and manage Foundry datasets from the terminal: `create`, `update`,
  `list`, `show`, `delete`, `versions list`, and `download`.
- Publishes a local `.jsonl` file or folder as a versioned dataset, picking the
  next version from what the project already carries. `--version` publishes at
  exactly that version instead, on `create` and `update` alike.
- `download` writes a registered version's content back to disk, reporting the
  version it chose when `--version` is omitted. It refuses a destination that
  already exists unless `--force` is passed. A forced replacement moves what was
  there aside first, and if the write fails and that cannot be put back, the
  error names where it still is rather than leaving the caller looking at a
  missing directory.
- Validates before sending: the rows are read before the first request, so a
  malformed row, an empty dataset, a name the service will not take, or a folder
  that could mean more than one dataset is refused against the file rather than
  from behind whatever the network happened to say first.
- `delete` asks before removing a version, and takes `--force` to skip the
  question. Where nobody can answer -- `--no-prompt`, `-o json`, or output
  redirected away from a console -- it asks for `--force` rather than assume,
  because a prompt nobody can see is a hang.
- `-o json` on every command for machine-readable output and `table` otherwise,
  with anything else refused rather than quietly answered in a format nobody
  asked for. `--no-prompt` for unattended use.
