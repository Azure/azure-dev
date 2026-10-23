# Release History

## Unreleased

- **Breaking:** the `rle.toml` `[harness]` table and the service `harness` object now use
  `type` (`gym` | `byoh`, lowercase, default `gym`), `endpoint` (https URL, required for
  `byoh`), and `protocol` (`responses` | `rle-invocation`, optional for `byoh`). The
  `type = "Gym"|"Harness"`, `subtype`, `agent_name`/`agentName`, `agent_version`/`agentVersion`,
  `invocation_url`/`invocationUrl`, and `baseUrl` keys are removed without aliases; manifests that
  set them fail with a message naming the replacement. Migration: `Gym/OpenEnv` -> `type = "gym"`;
  `Harness/HostedAgent` -> `type = "byoh"`, `protocol = "responses"`, `endpoint` = the agent's
  Foundry agent URL (`.../agents/<name>`; RLE appends the responses path); `Harness/BYOH` -> `type = "byoh"`, `protocol = "rle-invocation"`,
  `endpoint` = the former `invocation_url`.
- `rle.toml` gains a `[task]` table (`format = "json" | "harbor"`, default `json`) and
  `[harness]` accepts `type = "package"` with `name`. `name` is valid only with `package`. The
  CLI validates these.
- `azd ai rle init` replaces `--subtype`, `--agent-name`, `--agent-version`, and `--base-url`
  with `--protocol` and `--endpoint`; `--type` accepts `gym` or `byoh`.
- `azd ai rle list` and `show` display a `PROTOCOL` column instead of `SUBTYPE`.
- `azd ai rle rollout --task '{"name":"<published task>"}'` runs a Harbor task in an
  ACA sandbox with a harness package and returns the reward and trajectory. `rle.toml` declares
  `[task] format = "harbor"` with `[harness] type = "package"` and `name` (`terminus-2` or
  `opencode`); `azd ai rle publish --task-dir` uploads tasks. `package` now requires
  `task.format = "harbor"` and vice versa.

## 0.8.24-preview

- `rle.toml` no longer has an `environment_protocol` / `environmentProtocol` key. RLE always
  uses the MCP environment protocol, so the CLI no longer sends, verifies, or accepts it.
  Manifests that still set the key (breaking) must remove it; strict-mode parsing now rejects it.

- Training submissions now automatically forward a Foundry user token in
  `aml-user-token` for evaluation authentication, separate from the
  Cognitive Services request token. Token acquisition failures stop submission.

- Job monitors now read status from the Foundry project's RLE jobs API, using
  `GET /rl_environments/jobs/{jobId}?api-version=2025-11-15-preview` and Foundry
  authentication, instead of the fine-tuning or facade service. Standalone
  monitoring no longer creates a fine-tuning client or accepts `--endpoint`.
  Submission, input uploads and artifact streaming are unchanged.

- `azd ai rle train` no longer requires `--follow` to mirror a run's logs and
  metrics locally and open its job monitor: both now happen by default, the
  same way `rollout` already opens a dashboard by default. Pass `--no-follow`
  to skip both, submit the job, and exit immediately; the command always
  prints `azd ai rle monitor --job-id <id>` so the job can be reopened either
  way.

- In the job monitor's rollout list, the first (sequence) column is no longer
  blank for rollouts from the real fine-tuning service, which does not report
  a sampler sequence id: it now falls back to the rollout's position in the
  list.

## 0.8.23-preview

- `azd ai rle train` and `azd ai rle monitor --job-id` now read jobs submitted
  to the real fine-tuning service from the RLE service APIs. When neither
  `--endpoint` nor `RLE_TRAIN_ENDPOINT` is set, `train` opens the job monitor
  after submission and keeps it running until Ctrl+C; Ctrl+C stops monitoring,
  not the job. The monitor shows the job's registered config, its metrics and
  its rollout list, and loads a rollout's conversation and graph only when you
  open it. Everything is held in memory; no run files are written locally.
  `--follow`, `--logs-root` and `--output` are rejected on this path because
  they would have no effect. Jobs routed to a facade with `--endpoint` or
  `RLE_TRAIN_ENDPOINT` keep the existing behavior.

- In the real-service monitor, running rollouts in view refresh their status
  sooner, and "Refresh service data" waits for the refresh to finish and says
  when it did. With a rollout open, the button refreshes only that rollout.
  A rollout with no result, such as one that failed, opens its own page that
  explains why and summarizes what the service recorded.

- For development, setting `RLE_ENV_OVERRIDE` makes `train` send its value in
  the `rleEnvOverride` header, which pins the RLE environment the job runs
  against.

- `azd ai rle rollout` no longer accepts a caller-supplied agent input.
  `--agent-input` and `--agent-input-file` are removed, and the Execute
  Rollout request no longer carries an `agent_input` field: for Harness
  targets, RLE now derives the agent-visible input server-side from the
  sandbox's reset observation. Use `--task`/`--task-file` for the reset
  payload; it is the only input a rollout takes.

## 0.8.16-preview

- `azd ai rle rollout` now negotiates the v2 Execute Rollout WebSocket
  protocol and rotates submission connections during service deployments.
  Existing rollouts continue collecting progress and terminal results on their
  original draining connection while new work uses a replacement. Explicit
  `ServerDraining` rejections are retried with the same rollout ID, but
  ambiguous sends, disconnects, `ServerShuttingDown`, and duplicate rollout
  IDs are never replayed. V1 services remain supported.

- The job dashboard now shows the training run, not only the rollouts it
  recorded. `train --follow` already mirrors a run's artifacts locally; the
  monitor reads that mirror and adds what the job is -- environment, base model,
  Loom session, dataset counts and the hyperparameters the service actually
  applied -- above per-step charts of reward, task success, learning signal,
  gradient norm, entropy and KL, plus a tailed run log.

  A run that collapses or stops producing mixed groups is named in plain text
  with what to change, rather than left to be read off a line that went down.
  Showing the applied hyperparameters beside the curves is the point: facade
  defaults override recipe values, and a collapse is usually visible in them.

  `monitor --job-id` shows the same panels for any job followed on this machine
  and gained `--logs-root` to find its mirror. A job that was never followed
  here still lists its rollouts, which come from the service, with the run
  panels omitted.

- The run charts now fit their value axis to the readings, and a "Value axis"
  control above them switches back to the anchored view. A run that climbs
  inside a band spent most of an anchored panel drawing the empty space
  underneath it: reward moving 0.52 to 0.77 was squeezed into under a third of
  the panel's height, which is the movement the panel exists to show. Anchoring
  is still the safer reading and stays one click away, because 0.75 drawn full
  height looks like success rather than three quarters, so a declared range
  bounds a fitted window rather than being discarded by it.

- Watching a job is now called a job monitor rather than a rollout monitor, in
  the line the command prints and in the page's own heading and title. A job has
  steps, metrics and a run log behind it; a single captured rollout does not,
  and `rollout` keeps its own name.

  Charts are drawn as SVG by the page itself. The dashboard serves no external
  scripts and keeps its existing content security policy.

- `azd ai rle train`, `jobs` and `monitor --job-id` now default to the RLE
  training service instead of deriving a fine-tuning endpoint from the Foundry
  account. RLE jobs are not served by an account's own fine-tuning API yet, so
  the derived endpoint could not accept them and `--endpoint` had to be passed
  every time. `$RLE_TRAIN_ENDPOINT` names a different deployment, and
  `--endpoint` still wins over both.

  The hardcoded address is deliberately temporary: when an account's endpoint
  serves RLE jobs, removing it restores the derivation, which is still tested.

- `azd ai rle train` gained `--follow`, which mirrors a run's artifacts to the
  local disk while the job runs, into the layout the Loom cookbook's dashboard
  already discovers. An in-flight Foundry run can be opened in that dashboard
  without waiting for the job to finish. `--logs-root` chooses where mirrored
  runs land and defaults to `$LOOM_LOGS_ROOT`, else `~/loom-runs`.

  Following is resumable: re-running `--follow` for the same job continues from
  the bytes already on disk. A dropped stream is reported but still exits zero,
  because the job was accepted and is running on the service.

- Fixed `azd ai rle train --follow` dropping the artifact stream part way
  through a healthy run, with `read from the run stream: i/o timeout`. The
  service sends a data frame only when an artifact changes, and a single
  rollout can run for minutes without writing one, so the connection is held
  open by pings. gorilla replies to a ping without touching the read deadline,
  so the 90s deadline expired on a connection that was never dead. Pings now
  count as liveness.

- `azd ai rle train --follow` now also serves the rollout dashboard for the run
  it submitted and prints its address, so submitting a run and watching its
  rollouts is one command. The dashboard opens empty, because the first rollout
  of a run takes minutes, and fills in as the run records them. It stays up
  after the run finishes, which is when its rollouts are finally all there to
  read. `--no-browser` prints the address without opening a browser.

  Without `--follow` the command exits as soon as the job is accepted, so there
  is nothing left to serve a dashboard from; it names the `monitor --job-id`
  command that opens one instead. The rollout ids a run generates are not
  knowable ahead of time, so the job id is the only way back to them.

- `azd ai rle monitor --job-id` now keeps up with a run in progress. It
  previously read the rollout list once, so a dashboard opened during a run
  stopped at whatever had landed when it started. The page polls, and asks only
  for what was recorded after the rollouts it already holds, so the cost of a
  poll does not grow with the length of the run.
- `azd ai rle train` now reads its settings from the environment's own
  `rle.toml`. A new `[train]` section records the model, the training and
  validation dataset paths, the suffix and the max episode steps, and
  `[train.options]` records the training options, so a run is `azd ai rle train`
  from the environment folder rather than a line of flags nobody remembers. Any
  flag still overrides the manifest for a single run.

  `[train]` is separate from `[defaults]` on purpose: `publish` sends
  `[defaults]` to the service and refuses a local copy that has drifted, and
  published versions are immutable, so a value that changes from run to run
  cannot live there.

  `--model` and `--training-file` are no longer required flags, because the
  manifest can supply both. They are still required settings, and a run that
  resolves neither reports which one is missing and where it can be set.

- `rle.toml` may pin `imageTag` in `[rle]`. A Harness environment can be
  republished against the same, unchanged image, so the image tag and the
  environment version move apart, and build scripts pin the tag. The manifest is
  parsed in strict mode, so an environment that recorded one previously failed
  every command with a schema error.

- `azd ai rle train` gained `--task-count`, which trains on only the first N
  tasks of the dataset. Checking an environment end to end previously meant
  editing the dataset itself.

## 0.8.15-preview

- `azd ai rle rollout` now displays live Execute Rollout WebSocket progress,
  including elapsed lifecycle milestones, model-call and environment-action
  status, cumulative Gym rewards, and episode termination details. Progress
  sequence and partial-response correlation are validated, dropped-event gaps
  remain supported, and the terminal `completed` or `error` response stays
  authoritative.
- The local rollout dashboard now preserves and displays enriched Execute
  Rollout results. It includes a collapsed final-response preview, a captured
  model-conversation tab, tool activity, and a compact call timeline. Full
  response and conversation content remains available without overwhelming the
  summary, and older artifacts without enriched fields remain supported.
- Rollout artifacts now retain the service's `final_response`,
  `request_messages`, and `response_message` fields so the enriched dashboard
  can be reopened from saved results without another service request.
- Loom session polling now accepts both string and structured operation-error
  payloads, allowing the CLI to report the underlying service failure instead
  of a JSON decoding error.

## 0.8.14-preview

- `azd ai rle rollout` now sends the rollout over RLE's Execute Rollout
  WebSocket instead of the HTTP API. The Foundry data-plane gateway ends an
  Execute Rollout HTTP request after roughly 120 seconds, which is shorter than
  a real Harness rollout, so a rollout that actually succeeded came back as a
  408 carrying no result and no artifacts; the upgraded socket is not subject
  to that cut. The upgrade travels on the gateway-forwarded OpenEnv instance
  route, because the gateway does not route the rollout path. Where that route
  is not published the upgrade is refused before any rollout is requested, so
  the CLI reports it and falls back to the HTTP API unchanged. A failure after
  the upgrade is never retried on HTTP, because by then the rollout may already
  have run.

## 0.8.13-preview

- In development mode (`AZD_AI_RLE_ENABLE_ALL=true`), `azd ai rle rollout`
  now always opens the local rollout dashboard when execution finishes, and the
  `rollout --monitor` flag is removed. The dashboard is skipped, with a
  warning rather than a failed rollout, when the Loom session cannot be closed
  or the artifacts cannot be saved, and silently when `--output` is set.
  `azd ai rle monitor` is unchanged.
- `azd ai rle run` now fails immediately with an explanation when `rle.toml`
  declares anything other than `Gym`/`OpenEnv`. The command drives the
  container over the OpenEnv WebSocket session, which a harness container does
  not serve, so it previously built the image, started the container, and only
  then failed at the WebSocket handshake with a connection error that said
  nothing about the real problem. The error names the declared type and points
  at the publish-then-rollout loop that does exercise a harness RLE. The check
  also covers the `--watch` restart path, which reloads the manifest.
- `azd ai rle init --type Harness` no longer tells you to run `azd ai rle run`.
  That command drives the container over the Gym/OpenEnv WebSocket session,
  which a harness container does not serve, so it could only ever fail at the
  connection. The harness next steps now cover the loop that does apply: deploy
  the agent, point `rle.toml` at it, publish, and execute one rollout.
- Added `install-local.sh`, which builds the extension for the current platform
  and installs it straight into the azd extensions directory. Local iteration no
  longer needs a registry entry with published artifacts.
- `azd ai rle init` and `azd ai rle skill install` now install the RLE project
  skills into `.claude/skills` as well as `.agents/skills`. Claude Code
  discovers project skills only under `.claude/skills`, so the skills reached
  OpenAI Codex and GitHub Copilot but not Claude. Both directories are written
  from the same source and are kept identical, and a failure part way through
  restores every directory to what it was.
- `azd ai rle init --type Harness` now always copies the full working sample
  (agent + rle) for both BYOH and HostedAgent. The placeholder scaffold and the
  `--harness-source` flag that selected it are removed, along with the
  interactive "select a harness starting point" prompt, so every Harness init
  lands on something that runs end to end. `--agent-name`, `--agent-version`,
  and `--base-url` remain as optional overrides applied to the copied sample's
  `rle.toml`, and a BYOH init with no folder name now uses the sample's default
  folder instead of failing.

## 0.8.12-preview

- `azd ai rle init --type Harness` now defaults to copying a full working
  sample (agent + rle) for both BYOH and HostedAgent. The sample is the first
  and preselected interactive choice, and it is what `--no-prompt` picks when
  `--harness-source` is not supplied, so an unqualified init lands on something
  that runs end to end instead of a TODO-laden placeholder. Pass
  `--harness-source existing` for the previous placeholder scaffold.
  `--agent-name`, `--agent-version`, and `--base-url` still apply to the copied
  sample's `rle.toml`.
- Added `azd ai rle skill install`, which installs or updates the RLE project
  skills into the current project.

## 0.8.11-preview

- `azd ai rle rollout` now saves completed rollout artifacts under
  `.output/<rollout-id>/`, including a readable summary, the capture graph,
  model-call records, and per-sequence token arrays.
- Added a development-only local rollout dashboard. Use `rollout --monitor` to
  open it after execution or `monitor --rollout-id <id>` to reopen saved
  artifacts and inspect the rollout graph, sequence-aligned token metrics,
  rewards, capture statistics, diagnostics, and response JSON without another
  execution or Azure sign-in.

## 0.8.10-preview

- Fixed `azd ai rle init` failing with
  `Could not parse rle.toml: strict mode: fields in the document are missing in
  the target struct`. The manifest parser did not declare the
  `[defaults.gym_openenv]` table or `defaults.reinforcement.max_completion_tokens`,
  both of which the Gym/OpenEnv samples set, so scaffolding a sample produced a
  manifest the extension itself could not read back.
- `azd ai rle` manifests now support `defaults.gym_openenv.model_response_field`,
  naming the action property that receives the model completion verbatim, and
  `defaults.reinforcement.max_completion_tokens`, the output budget for a single
  model turn. Both are forwarded on `azd ai rle publish` and are validated to
  match the service contract.

## 0.8.9-preview

- `azd ai rle train`'s `--training-file` and `--validation-file` now take a path
  to a local dataset file instead of a pre-uploaded `file-...` ID. The extension
  uploads the file to the selected fine-tuning resource with the `fine-tune`
  purpose and submits the job with the returned file ID.
- Fixed a build break in `azd ai rle train`: a `LocalError` literal set an `Err`
  field that `azdext.LocalError` does not declare, so the extension failed to
  compile.

## 0.8.8-preview

- `azd ai rle rollout`'s underlying HTTP client timeout is now 300s (was
  30s). The 30s wall was masking real, longer-running RLE processing (a
  full Harness/BYOH rollout can take 60-90s+) as a blind
  "context deadline exceeded" with no server-side detail; 300s gives real
  rollouts room to return their actual result or a specific RLE error.

## 0.8.7-preview

- Preserve update-notice ordering after normal command output and display the
  update command on a separate line.
- Render non-breaking update notices in yellow, simplify the required-update
  error, and report optional registry-check failures only in debug mode.
- Read update metadata from the committed RLE development registry.
- `azd ai rle rollout` now forwards the resolved Foundry project endpoint on
  the executeRollout request (`model.project_endpoint`), matching Vienna's
  per-rollout Loom Capture Proxy routing. No new flag or env var is required:
  it reuses the same `FOUNDRY_PROJECT_ENDPOINT`/`--project-endpoint` value
  already resolved to build the RLE client for this call.

## 0.8.6-preview

- Block RLE lifecycle commands when the development registry marks a newer release
  as breaking, while leaving metadata and version commands available for updates and
  diagnostics.
- Show an update notice after normal command output when a newer non-breaking
  RLE extension is available.
- Rename `azd ai rle invoke` to `azd ai rle rollout` (no behavior change).

## 0.8.5-preview

- Version bump only; no functional changes since 0.8.4-preview.

## 0.8.4-preview

- `azd ai rle invoke`'s `--model` now falls back to rle.toml's `defaults.model.name` when omitted, so ad hoc rollouts against a source folder that already declares a model default only need `--task` (and, for Harness targets, `--agent-input`).

## 0.8.3-preview

- Align environment discovery and remote invocation with the refreshed RLE service routes and cursor-based response contracts.
- Use `/rl_environments` consistently for environment and instance lifecycle APIs.
- Manage remote invoke through temporary instance groups and instances instead of direct sandbox lifecycle APIs.
- Retry runtime creation while a published environment's disk image is still being prepared.
- Replace `.azd-rle.json` with a host-agnostic `rle.toml` manifest as the sole local RLE identity.
- Define RLE releases with control-plane `type` and `subtype` values: `Gym: OpenEnv`, `Harness: HostedAgent`, and `Harness: BYOH`.
- Pin local invoke to the manifest's `(name, version)` identity and require `--version` for source-free invocation.
- Derive an explicit service version bump from the manifest version and verify the published release identity.
- Tag published ACR images with the manifest RLE version rather than `latest`.
- Delete the temporary instance and group on exit with Ctrl+C-independent cleanup and concise terminal status.
- Authenticate and API-version OpenEnv gateway requests on the configured Foundry project origin, wait for runtime health before reporting readiness, and route the browser playground through an authenticated local proxy.
- Initialize a required local folder by interactively selecting and sparsely downloading an environment from the RLE samples repository, including its manifest.
- Filter Gym/OpenEnv samples by the samples repository's visibility catalog, and gate harness init targets, hidden samples, and other internal-only surfaces behind a single `AZD_AI_RLE_ENABLE_ALL` flag.
- Add `azd ai rle train` (experimental, gated behind `AZD_AI_RLE_ENABLE_ALL`) to submit an RLE-backed reinforcement fine-tuning job via finetunesapi's `rl_environment` method, using a published RLE environment as the reward source instead of a grader.
- Repurpose `azd ai rle invoke` from an interactive OpenEnv playground shell into a rollout executor: it now provisions a real Loom training session and sampler checkpoint, calls RLE's Execute Rollout API with a `--model` (required), `--task`/`--task-file`, and (for Harness targets) `--agent-input`/`--agent-input-file`, prints the reward/success/episode summary, and tears down the Loom session — so callers never handle Loom session or checkpoint identifiers directly.

## 0.3.0-preview

- Add `azd ai rle list` to list environments in the configured Foundry project.
- Add `azd ai rle show <environment-name>` to inspect an environment's full details and version history.
- Allow `azd ai rle invoke <environment-name>` to invoke an existing project environment without local source or state, with optional `--version` selection.
- Rename `azd ai rle deploy` to `azd ai rle publish` to avoid confusion with the core `azd deploy` command.
- Add `--version-bump` to `azd ai rle publish` so users can choose major, minor, or patch environment versioning.
- Use the Foundry project endpoint for project-relative RLE environment and sandbox APIs.
- Authenticate Foundry API requests with Azure credentials from `az login`, `azd auth login`, or another supported development credential.
- Send the required `2025-11-15-preview` Foundry data-plane API version.
- Support versioned environment deployments and sandbox `baseUrl` invocation.
- Wait for asynchronous disk-image conversion before leasing a sandbox and surface conversion failures directly.

## 0.1.0-preview

- Initial preview scaffold for the RLE extension with `init`, `run`, `invoke`, `deploy`, and `version` commands.