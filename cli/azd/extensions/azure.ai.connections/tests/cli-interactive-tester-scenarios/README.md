# Offline Connection authoring

The Tier 0 scenarios in `tier0/` check add, update, idempotency, and
service-key/name conflicts against real
project manifest without Azure access. It requires development builds of azd
and the `azure.ai.connections` extension installed in the tester's environment.
Use the cli-interactive-tester to load the scenario, run its pre-hook, and drive
the command and goals. The fixture is copied into a per-instance directory;
the source files remain unchanged.
