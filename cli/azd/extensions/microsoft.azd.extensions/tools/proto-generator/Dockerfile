# Run from cli/azd: mage generateProtos (prefers Docker, falls back to WSL Containers)
FROM golang:1.26-bookworm AS go-tools

ARG PROTOC_GEN_GO_VERSION=v1.36.11
ARG PROTOC_GEN_GO_GRPC_VERSION=v1.5.1

RUN GOBIN=/out go install "google.golang.org/protobuf/cmd/protoc-gen-go@${PROTOC_GEN_GO_VERSION}" \
    && GOBIN=/out go install "google.golang.org/grpc/cmd/protoc-gen-go-grpc@${PROTOC_GEN_GO_GRPC_VERSION}"

FROM node:22-bookworm-slim

ARG GRPCIO_TOOLS_VERSION=1.71.0
ARG GRPC_TOOLS_VERSION=1.13.1
ARG PROTOC_GEN_JS_VERSION=3.21.4
ARG PROTOC_VERSION=32.1
ARG TARGETARCH

RUN apt-get update \
    && apt-get install --yes --no-install-recommends \
        ca-certificates \
        curl \
        python3 \
        python3-venv \
        unzip \
    && rm -rf /var/lib/apt/lists/* \
    && case "$TARGETARCH" in \
        amd64) protoc_arch=x86_64 ;; \
        arm64) protoc_arch=aarch_64 ;; \
        *) echo "unsupported architecture: $TARGETARCH" >&2; exit 1 ;; \
    esac \
    && curl --fail --location --silent --show-error \
        --output /tmp/protoc.zip \
        "https://github.com/protocolbuffers/protobuf/releases/download/v${PROTOC_VERSION}/protoc-${PROTOC_VERSION}-linux-${protoc_arch}.zip" \
    && unzip -q /tmp/protoc.zip -d /usr/local \
    && rm /tmp/protoc.zip \
    && python3 -m venv /opt/proto-tools \
    && /opt/proto-tools/bin/pip install --no-cache-dir "grpcio-tools==${GRPCIO_TOOLS_VERSION}" \
    && npm install --global \
        "grpc-tools@${GRPC_TOOLS_VERSION}" \
        "protoc-gen-js@${PROTOC_GEN_JS_VERSION}"

COPY --from=go-tools /out/protoc-gen-go /usr/local/bin/protoc-gen-go
COPY --from=go-tools /out/protoc-gen-go-grpc /usr/local/bin/protoc-gen-go-grpc

ENV PATH="/opt/proto-tools/bin:${PATH}"

COPY --chmod=0755 generate-protos.sh /usr/local/bin/generate-protos

WORKDIR /workspace
ENTRYPOINT ["/usr/local/bin/generate-protos"]
