---
name: azd-grpc-contracts
license: MIT
metadata:
  version: "1.0"
description: >-
  **WORKFLOW SKILL** - Inspects and evolves azd extension protobuf contracts across
  the stable v1 and preview v1beta channels. Reports the current channel state and
  delta, validates v1beta additions, implements beta-only services or focused
  overrides, and safely graduates compatible functionality from v1beta to v1.

  INVOKES: go CLI, mage CLI, make, Buf CLI, git CLI, rg, bash/sh.

  USE FOR: add grpc API to v1beta, add preview extension contract, validate protobuf
  compatibility, compare v1 and v1beta, show contract delta, graduate beta API to v1,
  update generated grpc adapters, protobuf contract versioning.

  DO NOT USE FOR: general extension implementation without protobuf changes, REST API
  versioning, removing the legacy unversioned bridge without migration telemetry,
  changing v1 incompatibly, or creating an unrelated major v2 contract.
---

# azd gRPC contract lifecycle

Use this workflow as the safest and easiest path for changing the extension
protobuf API. It owns the relationship between:

- Stable source: `cli/azd/grpc/proto/azd/extensions/v1`
- Preview source: `cli/azd/grpc/proto/azd/extensions/v1beta`
- Generated contracts: `cli/azd/pkg/azdext/contracts/{v1,v1beta}`
- Stable SDK facade: `cli/azd/pkg/azdext`
- Generated beta adapters:
  `cli/azd/internal/grpcserver/versioned_services_generated.go`

Before changing these files, read:

1. `cli/azd/AGENTS.md`
2. `cli/azd/docs/extensions/contract-versioning.md`
3. `cli/azd/grpc/README.md`

Never edit generated protobuf, facade, or adapter files directly.

## Choose the workflow

| Request | Workflow |
|---|---|
| Explain the current channels or their differences | Inspect state and delta |
| Add a preview field, enum value, method, message, or service | Add to v1beta |
| Move a validated preview capability into stable | Graduate to v1 |
| Review a protobuf change | Validate the change |

{{ references/inspect-and-validate.md }}

{{ references/add-to-v1beta.md }}

{{ references/graduate-to-v1.md }}

## Required invariants

- `v1` is compatibility protected and changes only additively.
- Every stable symbol and method shape must have a compatible counterpart in
  `v1beta`; equivalently, `v1` must remain a subset of `v1beta`.
- A `v1beta` addition must not accidentally alter or remove an existing stable
  field, enum value, oneof, message, service, method, or streaming shape.
- Beta-only behavior on a shared service needs a focused generated override.
  Stable transcoding silently discards beta request fields unknown to `v1`.
- A beta-only service uses its native `v1beta` implementation rather than a
  stable adapter.
- Graduation adds a compatible shape to `v1`; it does not remove the beta route.
- Changing an existing public Go accessor from a `v1beta` return type to `v1`
  is source-breaking. Stop and get an explicit API migration decision before
  making that change.

## Exit criteria

- The requested contract shape exists in the correct channel.
- Stable-to-beta compatibility passes with an actionable result:
  `make proto-version-compatibility`.
- Historical compatibility and lint checks pass.
- Generated files are current.
- Runtime registration and any required focused overrides are covered by tests.
- SDK documentation and `requiredAzdVersion` are updated when consumers need a
  newer host.
