# Foundry project storage permissions in `azd ai agent doctor`

Source issue: [Azure/azure-dev#8665](https://github.com/Azure/azure-dev/issues/8665).

## Command and usage

Run the existing command from the current azd project. No additional parameters
are required for the proposed check:

```text
azd ai agent doctor
```

| Check | Requirement | Status |
| --- | --- | --- |
| User identity to Project | Required permissions to access the project | Existing |
| Project managed identity to relevant Storage | `Storage Blob Data Contributor` or a built-in role providing the same required access | Proposed |

- Apply this role check only to Storage accessed with the project managed
   identity. Skip account-key authentication and scenarios where no applicable
   customer-owned Storage is required. If the authentication mode or permissions
   cannot be verified, warn rather than report them as missing.
- Diagnose problems and suggest manual fixes only; do not grant permissions or
   create resources. Passing the role checks does not guarantee actual access or
   that `azd ai agent optimize` will succeed.

## Problem

Doctor currently checks whether the developer can access the project, but not
whether the project identity can access its storage. A job submitted with
`azd ai agent optimize` can fail in the cloud when it uses customer-owned storage
through that identity and the project lacks the required storage permissions.
The new check helps users identify and address this permission gap before running
the command.

## Proposed behavior

1. Identify the relevant Storage accounts from the current Foundry project's
   configuration and check the project managed identity's permissions on them,
   without checking unrelated storage accounts.
2. Recognize sufficient existing access, including `Storage Blob Data Owner` and
   inherited permissions. Do not ask users to add redundant roles or additional
   permissions beyond the stated requirement.
3. Explain whether the requirement is met, missing, not applicable, or unable to
   be verified. For a confirmed problem, identify who needs access, to which
   storage, and what action the user or administrator should take.

## Output

Proposed check name: `Project storage permissions`.

The new check appears in the existing `Remote` section. The labels below describe
outcomes; the terminal uses the existing status symbols.

| Result | Meaning |
| --- | --- |
| `PASS` | The project's role assignments meet the checked requirement for all relevant storage |
| `FAIL` | A required permission is confirmed missing, or the required project identity or storage configuration is invalid |
| `WARN` | The check could not determine whether the required access is configured |
| `SKIP` | Confirmed not applicable, or skipped by `--local-only` or existing prerequisites |

Keep the existing default presentation: passing checks show their name; failures
and warnings add a short explanation and any available `fix:` guidance; skipped
checks include the reason. The report summary and privacy protections remain
unchanged.

Proposed failure excerpt using the existing format, not captured output. Other
checks and the final report summary are omitted. Identifiers are placeholders:

```text
Remote
   (x) Project storage permissions
       Required storage role is missing for <project-identity> on <storage-account>.
      fix: Ask an administrator with permission to assign roles on <storage-account> to grant Storage Blob Data Contributor to <project-identity>.
```
