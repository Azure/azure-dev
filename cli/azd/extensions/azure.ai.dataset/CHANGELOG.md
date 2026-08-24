# Release History

## 1.0.0-beta.17 (2026-08-20)

First release of the Foundry datasets extension.

### Features Added

- Register and manage Foundry datasets from the terminal: `create`, `update`,
  `list`, `show`, `delete`, and `versions list`.
- Publishes a local `.jsonl` file or folder as a versioned dataset, picking the
  next version from what the project already carries. `--version` publishes at
  exactly that version instead, on `create` and `update` alike.
- Validates before sending: a malformed row, an empty dataset, a name the
  service will not take, and a folder that could mean more than one dataset are
  each refused locally.
- Reads dataset content back, whether the service hands out a blob URI or the
  container holding it.
- `delete` asks before removing a version, and takes `--force` to skip the
  question. Under `--no-prompt` or `-o json` it refuses rather than assume,
  because a prompt nobody can answer is a hang.
- `-o json` on every command, and `--no-prompt` for unattended use.
