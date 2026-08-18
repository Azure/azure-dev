# Release History

## 1.0.0-beta.12 (Unreleased)

First release of the Foundry datasets extension.

### Features Added

- Register and manage Foundry datasets from the terminal: `create`, `update`,
  `list`, `show`, `delete`, and `versions list`.
- Publishes a local `.jsonl` file or folder as a versioned dataset, picking the
  next version from what the project already carries.
- Validates before sending: a malformed row, an empty dataset, a name the
  service will not take, and a folder that could mean more than one dataset are
  each refused locally.
- Reads dataset content back, whether the service hands out a blob URI or the
  container holding it.
- `-o json` on every command, and `--no-prompt` for unattended use.
