# ADR-001: Extensions report named usage events, not declared fields

**Status:** Proposed

**Date:** 2026-07-30

## Context

Extensions need to report usage signals so the team can answer product
questions like which deployment mode people actually pick. Two earlier designs
were tried and rejected.

1. An allowlist of product fields (key, allowed values, eligible commands)
   inside `azd` core. Every new field an extension wanted became a core PR, a
   core release, and a wait for users to upgrade `azd` before any data arrived.
   It also put one product's vocabulary into the CLI that hosts all of them.
2. Moving that allowlist into the extension's registry entry, so the registry
   declared fields and core enforced them. This removed the core release
   dependency but kept the overhead: authors maintained a parallel declaration,
   core carried validation code for it, and every value set had to be enumerated
   ahead of time. `azd` core has the same "document your telemetry" problem for
   its own fields and intends to solve it with linting rather than a runtime
   allowlist, so extensions should not carry a second, heavier mechanism.

The constraint is therefore: extensions report freely within a bounded shape,
core owns identity and the attribute namespace, and content is governed the way
core governs its own fields.

That last part only works if there is a review to govern content. `ext.usage` is
the first path where an extension supplies the strings rather than the host
authoring every attribute, so the design also has to say which extensions get to
use it — otherwise unexpected text from an official extension lands in a
pipeline covered by `azd`'s privacy statement.

## Decision

**Extensions report a named event with a dynamic attribute map. `azd` core owns
identity and the attribute namespace, bounds the shape and the volume, and only
records events from extensions admitted to the official registry.**

- `ReportUsage(event_name, map<string, string> attributes)` replaces
  `ReportUsageAttribute(key, value)`. The extension names the event and supplies
  the attributes; nothing is declared in advance.
- The host writes `extension.id`, `extension.version`, and `extension.source`
  from the signed claims and the installed record, and `extension.event` from
  the caller's event name. Because none of the identity fields are on the wire,
  an extension cannot assert which extension it is.
- Every caller-supplied key is prefixed with `ext.` before it reaches the span.
  OpenTelemetry attributes overwrite on a duplicate key, so without the prefix
  an extension sending `extension.id` would overwrite the host's own value.
- Bounds are shape-only: at most 32 attributes, event name and keys at most 128
  UTF-8 bytes, values at most 512 UTF-8 bytes. No charset rules and no value
  enumeration.
- A separate volume bound caps recorded events at 100 per `azd` invocation. The
  shape bounds are per event and say nothing about how many arrive, and
  `ReportUsage` can be called in a loop. Budget is spent only by events that
  would otherwise be recorded, so a malformed call cannot starve a well-behaved
  one.
- Telemetry is not a capability. Capabilities describe customer-facing features
  the host calls into; telemetry is a service the host offers, like every other
  service on the extension gRPC API.
- Only extensions whose installed `azd` source has the reserved name, URL, and
  URL source type of the official registry are recorded. Attribute values are
  authored by the extension and are never reviewed at runtime, so registry
  admission is the thing that keeps unchecked third-party content out of a
  pipeline covered by `azd`'s privacy statement. This is a configuration-based
  source check, not a cryptographic provenance guarantee. A blank or polluted
  source is not treated as official, even though the upgrade resolver defaults
  a missing source to the main registry.
- A dropped event is not an error. An extension outside the official registry,
  and one past the event budget, get `Accepted: false` and no span. Reporting is
  best effort, so an author runs the same code path during local development as
  in production instead of having to swallow an error that only appears in one
  of them. The reason is written to the `azd` log, visible with `--debug`.
- `extension.source` is still recorded on the span. Once the verified source
  gate has passed it is a useful dimension rather than a filter.
- Accepted events are recorded on an `ext.usage` span rather than being appended
  to the command span.

Removing the runtime allowlist does not remove content responsibility.
Extension telemetry is subject to the same rules as core telemetry: fields are
documented, classified, never carry customer content, and go through privacy
review. That review happens when the extension is admitted to the official
registry, not on every call — which is exactly why admission is also the gate.

## Consequences

**Easier**

- Adding a signal is an extension change. Neither `azd` core nor the registry
  has to ship.
- `azd` core contains zero product semantics for extension telemetry, and no
  declaration validation code to maintain.
- A named event with individual attributes is directly queryable: filter on
  `extension.event`, then read `ext.*` from `customDimensions`. The previous
  one-key-per-call shape required stitching several spans together to
  reconstruct a single logical event.
- Everything reaching the pipeline came from an extension that passed registry
  admission, so the privacy story for `ext.*` is the extension review that
  admission already requires.

**More difficult**

- An admitted extension can still put high-cardinality or sensitive data in a
  value. The registry gate raises the floor but does not inspect content, so
  that remains a review and documentation problem rather than a runtime one,
  which matches how core fields are handled and is the direction core linting
  is heading.
- Extension authors cannot see their events land while developing against a
  locally installed build, because a `dev` or file-based source does not pass
  the gate. They can still verify the call path: `ReportUsage` succeeds and
  returns `Accepted: false`, and `--debug` says why. This is deliberate —
  pre-release data should not mix into production metrics — but it does mean
  first-party teams testing from a non-official registry get no data until
  the extension ships to the official one.
- The `ext.usage` span does not sit on the same row as the command in App
  Insights. Queries must join on `operation_Id`. This is reliable because `azd`
  does not sample and the exporter writes the trace ID to `operation_Id`.
- Trace context has to cross the gRPC boundary for that join to work, so the
  extension SDK forwards the W3C trace context headers and the server extracts
  them.
- Identity is derived from the installed record, which lives in user-writable
  config. An extension cannot assert its identity on the request, but this is
  not proof of provenance — it is the same trust level every existing capability
  gate already depends on.

## Alternatives Considered

**Keep the allowlist in core.** Simplest to review, and it keeps every value in
one Go file. Rejected because it puts one product's vocabulary in the CLI that
hosts all products, and because it forces a core release for each new field.

**Declare fields and allowed values in the registry entry.** The design this
ADR replaces. It removed the core release dependency but kept per-field
overhead in exchange for a guarantee that only holds for a non-tampered
install: telemetry ingestion is not authenticated per client, so a reviewed set
cannot be enforced from the client anyway. Governing content authoritatively
belongs downstream or in review, not in a runtime check.

**Gate reporting on the verified official registry source.** Adopted, after
review pushed back on shipping unchecked third-party strings into a pipeline
covered by `azd`'s privacy statement. The earlier position — that gating
`ext.usage` was inconsistent with `ext.run`, `ext.install`, and
`extension.installed` firing for any source — does not hold: every attribute on
those spans is authored by the host, so they observe an unofficial extension
without carrying its text. `ext.usage` is the first path where the extension
supplies the strings.

The caveat is that install source is a proxy for first party, not the same
thing. It answers "does the configured source match the official registry",
which only equals first party for as long as that registry stays first-party.
Future reviewed third-party extensions may therefore report. It is also a
client-side check against a record in user-writable config; that is acceptable
because editing it already implies local write access, which is a strictly
larger problem than telemetry.

**Rejecting an unofficial extension with an error.** Rejected in favour of
`Accepted: false`. An error forces every author to swallow a failure that
appears only during local development, which invites either noisy logging or a
blanket ignore that also hides real bugs. The response already carries an
`accepted` flag, so the outcome is stated rather than silent, and the drop
reason goes to the `azd` log.

**Make telemetry a capability.** Rejected per review feedback: capabilities
signal "this extension provides a customer-facing feature the host needs to
call", not "this extension consumes a host service".

**One RPC per attribute.** The previous shape. Rejected because a logical event
with several attributes arrived as several unrelated spans, which is both more
round trips and harder to query.

**Augment the existing command span instead of creating `ext.usage`.** Rejected:
it requires a process-global command-usage scope stack that has to be opened and
closed by middleware and kept correct across nested and concurrent commands.

**Verify a signed registry manifest on every report.** Rejected as
disproportionate. It adds key management, rotation, and an offline story to a
best-effort telemetry path, and would still only harden one client-side hop.
Editing the installed record already implies local write access, which is a
strictly larger problem than telemetry.
