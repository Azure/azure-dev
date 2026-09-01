# Release History

## 1.0.0-beta.18 (2026-08-31)

### Bugs Fixed

- `create` and `update` read the rows before the first request, which is what
  the previous release said they did. A malformed or empty `--from-file` was
  parsed during the upload, past the read that decides whether the name is
  taken, so the author was told whatever the network said first: pointed at an
  endpoint that does not resolve, a bad row came back as a DNS failure naming a
  host they had not mistyped, about a file they had.
- `delete` asks for `--force` when it cannot ask anything else. With output
  redirected there is no console to draw the confirmation on, and that surfaced
  as `rpc error: code = Unknown desc = The handle is invalid` -- a transport
  detail in place of the one thing the caller could act on.

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
