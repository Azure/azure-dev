# Release History

## 1.0.0-beta.5 (Unreleased)

### Features Added

- Initial release. `create`, `update`, `list`, `show`, `delete`, and
  `versions list`.
- The CRUD groups moved here from `azure.ai.evaluations`. Generation stayed
  there: `dataset generate` writes the `datasets:` entry in `evals/eval.yaml`,
  and that file belongs to the evaluation extension.

### Bugs Fixed

- `create --from-file` uploads the file that was named rather than a
  neighboring one, and no longer sends the byte order mark Windows writes.
- A malformed row, an empty dataset, a name the service will not take, and a
  folder that could mean more than one dataset are each refused locally,
  before a request is sent.
- A missing `--from-file` path is reported as a missing path rather than as
  the syscall that discovered it, and `-o json` stays parseable when a
  command fails.
- An expired credential is reported as something to retry before it is
  reported as a reason to sign in again.
- `versions list` names the dataset it looked under when it finds none, and
  an unknown name is answered the way the sibling extension answers it.
- Fixed a panic and a leaked response body on the error paths.
