# Run from cli/azd: mage generateProtos (prefers Docker, falls back to WSL Containers)
FROM mcr.microsoft.com/oss/go/microsoft/golang:1.26.4-azurelinux3.0 AS go-tools

ARG PROTOC_GEN_GO_VERSION=v1.36.11
ARG PROTOC_GEN_GO_GRPC_VERSION=v1.5.1

RUN GOBIN=/out go install "google.golang.org/protobuf/cmd/protoc-gen-go@${PROTOC_GEN_GO_VERSION}" \
    && GOBIN=/out go install "google.golang.org/grpc/cmd/protoc-gen-go-grpc@${PROTOC_GEN_GO_GRPC_VERSION}"

FROM mcr.microsoft.com/azurelinux/base/nodejs:24

ARG GRPCIO_TOOLS_VERSION=1.71.0
ARG GRPC_TOOLS_VERSION=1.13.1
ARG PROTOC_GEN_JS_VERSION=3.21.4
ARG PROTOC_VERSION=32.1
ARG TARGETARCH

# Microsoft's Go crypto backend needs the C toolchain in the final image too.
RUN tdnf install -y \
        binutils \
        ca-certificates \
        curl \
        gcc \
        glibc-devel \
        kernel-headers \
        make \
        python3 \
        unzip \
    && tdnf clean all \
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
COPY --from=go-tools /usr/local/go /usr/local/go

ENV CGO_ENABLED=1
ENV GOCACHE=/tmp/go-build
ENV GOMODCACHE=/tmp/go-mod
ENV HOME=/tmp
ENV PATH="/usr/local/go/bin:/opt/proto-tools/bin:${PATH}"

COPY --chmod=0755 generate-protos.sh /usr/local/bin/generate-protos
COPY --chmod=0755 validate-scaffold-compatibility.py /usr/local/bin/validate-scaffold-compatibility.py

WORKDIR /workspace
ENTRYPOINT ["/usr/local/bin/generate-protos"]
