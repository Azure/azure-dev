# Extension Framework

> **NOTE:** The `azd` extension framework is currently an ALPHA feature.

## Code Generation

`azd` leverages gRPC for the communication protocol between Core `azd` and extensions. gRPC client & server components are automatically generated from profile files.

- Proto files @ [grpc/proto](../grpc/proto/)
- Generated files @ [pkg/azdext](../pkg/azdext)
- Make file @ [Makefile](../Makefile)

To re-generate gRPC clients run `make proto`

## gRPC Services

### Project Service

Exposes read-only services to inspect `azd` project configuration.

See [project.proto](../grpc/proto/project.proto) for more details.

### Environment Service

Exposes read/write services to interact with `azd` environments and values.

See [environment.proto](../grpc/proto/environment.proto) for more details.

### User Config Service

Exposes read/write services to interact with `azd` global user configuration.

See [user_config.proto](../grpc/proto/user_config.proto) for more details.

### Deployment Service

Exposes read services to inspect `azd` Azure deployments

See [deployment.proto](../grpc/proto/deployment.proto) for more details.

### Prompt Service

Exposes a suite of services to interact with users with common UX components.

See [prompt.proto](../grpc/proto/prompt.proto) for more details.

