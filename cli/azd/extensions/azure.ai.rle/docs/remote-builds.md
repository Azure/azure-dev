# Remote image builds

`azd ai rle publish --remote` uploads the Docker build context to Azure Container
Registry (ACR), builds a Linux x86_64 image with ACR Tasks, and pushes the result
before registering the RLE version. Local builds remain the default.
`run` still uses the local container runtime.

For full `.tar.gz` task archives, resumable parallel ACR preparation, or supplying
already-pushed images, see [Harbor task artifact manifests](harbor-artifacts.md).

```sh
azd ai rle publish --remote
azd ai rle publish --remote --dockerfile server/Dockerfile
azd ai rle publish --remote --task-dir ./task-a --task-dir ./task-b
```

## Prerequisites and costs

Install Azure CLI and sign in with `az login`. Select the subscription containing
the existing registry with `az account set --subscription <subscription-id>`.
Configure `FOUNDRY_PROJECT_ENDPOINT` and `AZURE_CONTAINER_REGISTRY_ENDPOINT` as
for a local publish. The caller must be allowed to submit ACR quick builds, and
the builder must be able to pull the base images and reach any Dockerfile
dependencies. Existing RLE image-pull and project permissions still apply.
No local Docker daemon or `az acr login` is needed for remote builds.

ACR Tasks uses paid build compute, in addition to normal registry storage and
applicable network charges. This option does not provision a registry, change
permissions, create a dedicated agent pool, or enable recurring tasks.
Builds may queue when the registry's task concurrency limit is reached.

## Context and image identity

For a normal environment, publish uploads the source folder as its build context,
using the same Dockerfile selection and path validation as local builds. Review
the context and `.dockerignore` before uploading; keep credentials and populated
`.env` files out of build contexts.

For Harbor tasks, only each task's `environment/` directory is a build context.
Instructions and verifier files are still handled separately as task bundles.
Image names and environment-content hashes are unchanged. ACR builds and pushes
the image directly, so publish does not subsequently run a local image push.
Multiple task directories are still processed sequentially.

## Caching

Do not assume a warm-up quick build warms the workers used by later ACR builds.
Separate quick builds can execute on workers without the previous build's local
Docker layer cache. A registry reporting that a layer already exists during push
is upload deduplication, not proof that Docker build steps were cached.

Remote builds do not currently configure an explicit shared build cache. ACR can
reuse stored image layers during push, and unchanged Harbor bundles still skip
upload, but cross-run build-cache hits are not guaranteed. Use the run's build
logs to verify cache reuse before drawing timing or scaling conclusions.

## Monitoring and interruption

Publish streams Azure CLI build logs, including the ACR run ID, and waits for
the build and push to finish before uploading Harbor bundles or registering the
environment version. A failed remote build is an error; it does not silently
fall back to a local build.

Stopping the local command does not guarantee that the ACR run stopped. Inspect
the recorded run before retrying so an interruption does not create duplicate
builds:

```sh
az acr task list-runs --registry <registry>
az acr task show-run --registry <registry> --run-id <run-id>
az acr task logs --registry <registry> --run-id <run-id>
```

Remote runs may continue to incur build charges after the local client exits.
Image deletion, build cancellation, and stopping local log streaming are separate
operations. This option does not automatically delete images or cancel runs.
