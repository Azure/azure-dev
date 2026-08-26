PROTO_ROOT := grpc/proto
PROTO_INCLUDE := grpc/include
PROTO_V1_DIR := $(PROTO_ROOT)/azd/extensions/v1
PROTO_V1BETA_DIR := $(PROTO_ROOT)/azd/extensions/v1beta
CONTRACTS_V1_DIR := pkg/azdext/contracts/v1
CONTRACTS_V1BETA_DIR := pkg/azdext/contracts/v1beta
FACADE_FILE := pkg/azdext/contracts_v1_aliases.go
ADAPTER_FILE := internal/grpcserver/versioned_services_generated.go
GO_MODULE := github.com/azure/azure-dev/cli/azd

PROTOC_VERSION := libprotoc 32.1
PROTOC_GEN_GO_VERSION := protoc-gen-go v1.36.10
PROTOC_GEN_GO_GRPC_VERSION := protoc-gen-go-grpc 1.5.1
BUF ?= buf

.PHONY: all proto proto-check-tools proto-lint proto-breaking clean

all: proto

proto: proto-check-tools
	mkdir -p $(CONTRACTS_V1_DIR) $(CONTRACTS_V1BETA_DIR)
	rm -f $(CONTRACTS_V1_DIR)/*.pb.go $(CONTRACTS_V1BETA_DIR)/*.pb.go
	protoc --proto_path=$(PROTO_ROOT) --proto_path=$(PROTO_INCLUDE) \
	       --go_out=. --go_opt=module=$(GO_MODULE) \
	       --go-grpc_out=. --go-grpc_opt=module=$(GO_MODULE) \
	       $(PROTO_V1_DIR)/*.proto $(PROTO_V1BETA_DIR)/*.proto
	go run ./grpc/generatefacade \
		-source $(CONTRACTS_V1_DIR) \
		-output $(FACADE_FILE)
	go run ./grpc/generateadapters \
		-stable-source $(CONTRACTS_V1_DIR) \
		-beta-source $(CONTRACTS_V1BETA_DIR) \
		-output $(ADAPTER_FILE)

proto-check-tools:
	@test "$$(protoc --version)" = "$(PROTOC_VERSION)" || \
		(echo "protoc must be $(PROTOC_VERSION)" && exit 1)
	@test "$$(protoc-gen-go --version)" = "$(PROTOC_GEN_GO_VERSION)" || \
		(echo "protoc-gen-go must be $(PROTOC_GEN_GO_VERSION)" && exit 1)
	@test "$$(protoc-gen-go-grpc --version)" = "$(PROTOC_GEN_GO_GRPC_VERSION)" || \
		(echo "protoc-gen-go-grpc must be $(PROTOC_GEN_GO_GRPC_VERSION)" && exit 1)

proto-lint:
	$(BUF) lint grpc

proto-breaking:
	@test -n "$(BUF_BREAKING_AGAINST)" || \
		(echo "set BUF_BREAKING_AGAINST to a Buf module, image, or Git source" && exit 1)
	$(BUF) breaking grpc --against "$(BUF_BREAKING_AGAINST)"

clean:
	rm -f $(CONTRACTS_V1_DIR)/*.pb.go $(CONTRACTS_V1BETA_DIR)/*.pb.go $(FACADE_FILE) $(ADAPTER_FILE)
