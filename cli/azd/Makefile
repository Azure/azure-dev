PROTO_DIR=grpc/proto
GEN_DIR=pkg/azdext

.PHONY: all proto clean

all: proto

proto:
	# Generate server-side code
	mkdir -p $(GEN_DIR)
	# Generate code for all .proto files into the same package
	protoc --proto_path=$(PROTO_DIR) \
	       --go_out=$(GEN_DIR) --go_opt=paths=source_relative \
	       --go-grpc_out=$(GEN_DIR) --go-grpc_opt=paths=source_relative \
	       $(PROTO_DIR)/*.proto

clean:
	rm -rf $(GEN_DIR)