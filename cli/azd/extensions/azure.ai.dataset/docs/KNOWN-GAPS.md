# Known gaps

Open work on `azd ai dataset`, written down so it is decided rather than
rediscovered.

## 1. Two request shapes are unverified against the datasets contract

Both were raised in review on the pull request, both are plausible, and neither
was changed, because every dataset in the last two bug bash builds was created
through these exact calls. Changing a verified-working request on an unverified
contract description is how a working upload path becomes a broken one.

### 1a. `startPendingUpload` sends an empty body

`StartPendingUpload` posts `{}`. The review says `pendingUploadType` is required
at `2025-11-15-preview`.

The evidence in this repository points two ways. `azure.ai.models` sends
`TemporaryBlobReference`, added deliberately per its changelog.
`azure.ai.training` sends `BlobReference` and comments it as "Always". Our
request model already carries the field; only the value is missing.

Sending a guessed enum is the one change here that can turn a working call into
a 400. The response echoes `pendingUploadType`, so the way to settle it is to
capture what the service picks on a real upload and send exactly that.

### 1b. Finalization may not match `Datasets_CreateOrUpdateVersion`

`FinalizeDatasetVersion` sends `PUT` with `application/json` and a body of
`{name, version, type, dataUri}`. The review says the contract is `PATCH` with
`application/merge-patch+json`, and that `name`, `version` and `isReference` are
read-only.

Not confirmed: the specs repo reachable from here carries no datasets spec, so
neither the verb, the content type, nor the read-only fields could be checked.

The underlying point holds regardless — sending fields the caller does not own
is a latent break, and it costs nothing to stop once the shape is confirmed. If
a backend does tighten this, the failure is every upload.

Verify against the spec and one live call, then change both together.
