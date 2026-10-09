# Offline preview host integration

From the repository root, run:

```powershell
.\cli\azd\extensions\azure.ai.agents\tests\host-preview\run.ps1
```

The runner builds agents and projects test processes using their published azd
SDK dependencies. It runs the unchanged checkout's gRPC host with extension-local test fixtures
injected through Go's `-overlay` option. No file is written into the core source
tree, no module replacement is used, and no Azure resources or persistent user
configuration are accessed.

The test exercises stable registration, beta preview registration and forwarding,
fresh providers without `Initialize`, create/update/no-change/unknown results,
six-group presence-aware comparison, omitted normalization-only defaults and
absent/code-only/unchanged container groups, conditional build/push booleans,
image-reference updates, authored metadata tag list additions/updates/removals
and unchanged omission through production request mapping, ignored
code/session/artifact differences, safe values/redaction, and legacy errors.
It also uses the projects extension's production registration
to verify a silent no-op preview, without project/environment/account reads.
The forwarded project result is passed to the unchanged deploy preview command
to reproduce the previous warning and verify that no warning or project message
remains in readable/JSON output. The host-owned successful progress row remains;
project JSON explicitly says `noOp`, `deployment`, and no infrastructure preview.
Foundry reads and host project/environment/tenant reads for agent previews are
stubbed; the real host stream, authentication middleware, contract conversion,
and published SDK preview lifecycle run end to end. All other host services are
unimplemented, so an unexpected extension write or prompt fails. The projects
fixture enables provisioning, validation, and event registration only; no such
operation or lifecycle hook is invoked.

`AZD_PREVIEW_EXTENSION_TEST_BINARY` selects the temporary test executable for the
host fixture, and `AZD_PREVIEW_EXTENSION_PROCESS=true` activates its subprocess
helper. The matching `AZD_PROJECT_PREVIEW_EXTENSION_TEST_BINARY` and
`AZD_PROJECT_PREVIEW_EXTENSION_PROCESS` select the project helper, while
`AZD_PROJECT_PREVIEW_TEST_RESULT` carries its forwarded result between tests.
These are test-only inputs, set by the runner/fixture, not product settings.
The runner uses `GOWORK=off` and the repository-pinned Go 1.26.4 toolchain, restores
the caller's environment, and removes its temporary binaries, result, and overlay.
