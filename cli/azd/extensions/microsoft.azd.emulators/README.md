# Azure Emulators extension

Launch first-party Azure service emulators and print the connection settings needed by a local application. This is an **alpha**, development-only extension, not a way to host production Azure services.

## Build and install locally

Prerequisites: an azd build with beta `add-provider` support (such as the `azd-add-provider` branch), the repository's Go toolchain, and Docker running **Linux containers** with Docker Compose 2.23.1 or later. SignalR, Static Web Apps and Web PubSub also require Docker image-build support. Images and packages are downloaded on first use.

From this directory:

```bash
azd extension install microsoft.azd.extensions
azd x build --skip-install
azd x pack --bundle --output ./bin/emulators.zip
azd extension install ./bin/emulators.zip
azd emulators --help
```

These commands build and install a self-contained bundle for the current platform. After the initial installation, `azd x build` is sufficient to rebuild the installed binary. There are no published registry artifacts for this extension yet.

For standalone development without installing into azd:

```bash
go build -o ./bin/emulators .
./bin/emulators storage
```

On Windows, use `.\bin\emulators.exe`. `build.sh` and `build.ps1` produce the standard extension artifact names for all six supported OS/architecture pairs. Set `EXTENSION_PLATFORM` to a single pair, such as `linux/arm64`, to build just that target.

## Add emulators to a project

After installing the extension, run `azd add` and choose an item in the **Emulators** category. This edits `azure.yaml` without starting Docker. Subsequent selections go into the same `emulators` infrastructure entry:

```yaml
infra:
  layers:
    - name: emulators
      provider: emulators
      config:
        storage: {}
        staticwebapps:
          directory: dist
```

The provisioning phase of `azd up` (or `azd provision`) starts the declared emulators locally and waits for readiness. Docker is needed at that point, not at `azd add`. These commands still have azd's normal Azure sign-in requirements, though the emulator provider itself creates no Azure resources. `azd down` stops and removes the declared emulators and their local data for that project and environment. Run it before removing a declaration you no longer need.

For this alpha release, provisioning a declared emulator accepts its required license terms by default without prompting. Set `acceptLicense: false` under the emulator's key in `azure.yaml` to block provisioning and see the terms. Standalone commands still require `--accept-license` where applicable.

`azd add` shows unavailable emulators as disabled, with the reason. Provisioning checks the same requirements before starting any emulator in the layer: Docker must be available, `docker compose` must be version 2.23.1 or later, and the Docker daemon must run Linux containers. Service Bus and Azure Data Explorer (Kusto) also require an AMD64 daemon. The extension does not install anything or use the standalone `docker-compose` command.

The add menu writes `infra.layers` or `layers[].infra`, depending on the project's layout. Move existing flat infrastructure into a layer first. Project instances use the default ports and separate state from the standalone commands below. Default ports are still shared across the machine.

## Commands

Each emulator has its own command. Launches wait for running containers and readiness checks before printing connection information.

| Command | Default host ports | Connection information |
|---|---|---|
| `azd emulators storage` | 10000 (Blob), 10001 (Queue), 10002 (Table) | Storage connection string and service endpoints |
| `azd emulators cosmosdb` | 8081 (HTTPS gateway), 1234 (Data Explorer), 18081 (health) | NoSQL gateway endpoint, development key, connection string, explorer URL |
| `azd emulators servicebus --accept-license` | 5672 (AMQP), 5300 (management) | Data/admin connection strings, queue, topic and subscription |
| `azd emulators eventhubs --accept-license` | 5673 (AMQP), 9092 (Kafka), 5301 (health) | AMQP connection string, event hub, consumer group and Kafka SASL settings |
| `azd emulators appconfig` | 8483 | Anonymous App Configuration endpoint and web UI |
| `azd emulators kusto --accept-license` | 8085 | Kusto endpoint and unauthenticated connection string |
| `azd emulators durabletask` | 8080 (scheduler), 8082 (dashboard) | Scheduler connection string, task hub and dashboard URL |
| `azd emulators signalr` | 8888 | Serverless SignalR connection string |
| `azd emulators staticwebapps --directory ./dist` | 4280 | Website URL and served directory |
| `azd emulators webpubsub` | 8084 | Preview Web PubSub connection string |
| `azd emulators sqldb --accept-license` | 1433 | Preview Azure SQL Database connection string and generated SQL credentials |

Default host ports do not overlap, and dependencies have no published ports. Each emulator runs in an isolated Compose project. All published ports bind to `127.0.0.1`, not external interfaces. Printed settings are for applications running on the host.

### Connection output and automation

After `azd up` or `azd provision`, connection settings for the emulator layer are available in the active azd environment. The keys use `AZD_EMULATOR_<EMULATOR>_<SETTING>` to avoid overwriting cloud resource outputs or another emulator's settings. For example:

```bash
azd env get-value AZD_EMULATOR_STORAGE_AZURE_STORAGE_CONNECTION_STRING
azd env get-value AZD_EMULATOR_EVENTHUBS_EVENTHUBS_CONNECTION_STRING
```

These settings can include development credentials. Keep your azd environment files private and use these values only for local development. `azd down` removes the settings for the emulator instances it stops.

```bash
azd emulators storage
azd emulators servicebus --accept-license --output json
azd emulators storage --output none
```

Standalone command output lists named connection settings. JSON output contains `emulator`, `status`, `projectName`, `connections` (an array of `{ "name": "...", "value": "..." }`) and optional `notes`. Progress goes to stderr, so stdout can be piped directly to a JSON parser. `--output none` suppresses normal output. Standalone commands do not write credentials into an azd environment.

Service Bus creates `queue.1`, `topic.1` and `subscription.1`. Its administration connection string uses the management port, not the AMQP port. Event Hubs creates `eh1` with two partitions and consumer group `cg1`. Use the printed Kafka SASL password for Kafka clients, not the AMQP connection string: the emulator uses its endpoint to advertise the Kafka listener.

### Reuse, ports and cleanup

```bash
azd emulators storage --port 11000 --queue-port 11001 --table-port 11002
azd emulators storage
azd emulators storage --stop
```

Repeating a launch reuses the saved configuration, container data and generated passwords. Explicitly changing a saved port, directory or upstream URL is rejected. Stop the instance before launching it with different settings. The normal default launch timeout is five minutes, including image pulls and builds. Use `--timeout 10m` for a slow first launch.

`--stop` removes the emulator's containers, dependencies and anonymous volumes, then deletes its saved state. **All emulator data is discarded.** Downloaded images and build cache remain. Launch-only flags cannot be combined with `--stop`.

State is private to the azd configuration directory, under `emulators/` (normally `~/.azd/emulators/`, respecting azd's existing `AZD_CONFIG_DIR` override). SQL passwords and SignalR upstream configuration are stored there with private file permissions. Standalone commands keep one saved instance per emulator per azd configuration directory, shared across projects and environments. Explicit `--environment` flags are rejected by those commands. Standalone commands do not require Azure login or a subscription.

If launch fails, no successful connection result is printed. Partial containers and state remain available for diagnosis and `--stop` cleanup. The error includes the isolated Compose project name when readiness fails.

### SignalR upstream

```bash
azd emulators signalr \
  --upstream-url http://host.docker.internal:7071/runtime/webhooks/signalr
```

The default upstream is the URL above. It is resolved **inside** the container. `localhost` would refer to the emulator container, not the host. The extension adds Docker's `host.docker.internal` host-gateway mapping.

### Limits and platform requirements

These are Microsoft's development/test emulators, with their upstream limitations:

- **Storage:** Azurite emulates Blob, Queue and Table Storage. Table support is preview. Azure Files and Data Lake Storage Gen2 are not included.
- **Cosmos DB:** Uses the vNext emulator's NoSQL gateway API, not direct mode or other Cosmos DB APIs. Authentication is not enforced. Trust its self-signed HTTPS certificate in the local SDK.
- **Service Bus:** Includes a SQL Server 2022 dependency. The extension requires a native AMD64 Docker daemon and does not probe x64 emulation on ARM64. Emulator data is not durable across restarts. Microsoft's native administration SDK support is limited to .NET.
- **Event Hubs:** Includes a private Azurite dependency. Data is not durable across restarts, and Kafka support is limited to producer/consumer scenarios.
- **App Configuration:** Enables anonymous owner access for local SDKs that support anonymous authentication.
- **Kusto:** Requires an x64 CPU with SSE4.2 and AVX2. Managed ingestion and authentication are not available.
- **Durable Task:** Uses the emulator's `default` task hub and unauthenticated scheduler endpoint.
- **SignalR:** Supports serverless scenarios and transient transport, not a replacement for the hosted SignalR service.
- **Static Web Apps:** Serves the selected static-content directory read-only with local routing and mock authentication. It does not launch a Functions API.
- **Web PubSub:** Uses Microsoft's beta emulator. State is in memory and message TTL expiration is not enforced.
- **Azure SQL Database:** Uses the preview local Azure SQL Database engine, including native x64/ARM64 images. It is not a full managed-service replica. `sa` credentials and `TrustServerCertificate=True` are for local development.

Review the upstream license URLs shown by commands that require `--accept-license`. Pass the flag only after accepting the emulator and dependency terms. Direct `azd emulators` commands never prompt for license confirmation, so behavior is identical in scripts and with `--no-prompt`.

Legacy Windows Cloud Services, the archived Event Grid emulator and third-party emulators are outside this extension's scope.

## Development

The shared lifecycle/state/readiness code lives in `internal/emulator/`. Each service has a separate implementation in `internal/emulators/`. Command registration and output live in `internal/cmd/`.

After a successful `azd add`, the listener exits quietly if azd resets the RPC connection during shutdown. Other listener errors are still reported.

```bash
# From cli/azd
go test ./extensions/microsoft.azd.emulators/...
go test -race ./extensions/microsoft.azd.emulators/...
golangci-lint run ./extensions/microsoft.azd.emulators/...
```

The service-first shape (`azd emulators <service>`) intentionally follows this extension's launcher interface rather than the verb-first preference in the [extension style guide](../../docs/extensions/extensions-style-guide.md).
