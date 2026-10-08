# Offline preview host integration

From the repository root, run:

```powershell
.\cli\azd\extensions\azure.ai.agents\tests\host-preview\run.ps1
```

The runner builds an extension test process using its published azd SDK dependency.
It runs the unchanged checkout's gRPC host with the extension-local test fixture
injected through Go's `-overlay` option. No file is written into the core source
tree, no module replacement is used, and no Azure resources or persistent user
configuration are accessed.

The test exercises stable registration, beta preview registration and forwarding,
fresh providers without `Initialize`, create/update/no-change/unknown results,
and legacy errors. Foundry reads and host project/environment/tenant reads are
stubbed; the real host stream, authentication middleware, contract conversion,
and published SDK preview lifecycle run end to end. All other host services are
unimplemented, so an unexpected extension write or prompt fails.

`AZD_PREVIEW_EXTENSION_TEST_BINARY` selects the temporary test executable for the
host fixture, and `AZD_PREVIEW_EXTENSION_PROCESS=true` activates its subprocess
helper. Both are test-only inputs, set by the runner/fixture, not product settings.
The runner uses `GOWORK=off` and the repository-pinned Go 1.26.4 toolchain, restores
the caller's environment, and removes its temporary binary and overlay.
