# Release History

## 1.0.0-beta.1 (Unreleased)

First release of the Foundry datasets extension.

### Features Added

- Register and manage Foundry datasets from the terminal: `create`, `update`,
  `list`, `show`, `delete`, and `versions list`.
- Publishes a local `.jsonl` file or folder as a versioned dataset, picking the
  next version from what the project already carries. `--version` publishes at
  exactly that version instead, on `create` and `update` alike.
- Validates before sending: the rows are read before the first request, so a
  malformed row, an empty dataset, a name the service will not take, or a folder
  that could mean more than one dataset is refused against the file rather than
  from behind whatever the network happened to say first.
- Reads dataset content back, whether the service hands out a blob URI or the
  container holding it.
- `delete` asks before removing a version, and takes `--force` to skip the
  question. Where nobody can answer -- `--no-prompt`, `-o json`, or output
  redirected away from a console -- it asks for `--force` rather than assume,
  because a prompt nobody can see is a hang.
- `-o json` on every command for machine-readable output and `table` otherwise,
  with anything else refused rather than quietly answered in a format nobody
  asked for. `--no-prompt` for unattended use.
