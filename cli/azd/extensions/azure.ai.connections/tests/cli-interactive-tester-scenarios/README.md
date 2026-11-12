# Offline Connection authoring

The Tier 0 scenarios in `tier0/` check add, update, idempotency, and
service-key/name conflicts against real
project manifest without Azure access. It requires development builds of azd
and the `azure.ai.connections` extension installed in the tester's environment.
Use the cli-interactive-tester to load the scenario, run its pre-hook, and drive
the command and goals. The fixture is copied into a per-instance directory;
the source files remain unchanged.

Authoring uses the existing Project APIs. New services use `AddService` for
the declaration and a service setter for any literal environment bindings;
updates change only managed top-level fields. Static
validation covers the complete candidate project before any mutation, but
multiple update requests are not a transaction. Transport failures can leave
a partial update; inspect the manifest and rerun to reconcile it. Fault-injection
unit tests cover those failures separately from these direct CLI scenarios.
