#!/bin/sh
# (NOTE: this script runs _inside_ of the container instance. Run the magefile generateProtos target to generate language files)
set -eu

repo_root="${1:-/workspace}"
azd_dir="$repo_root/cli/azd"
languages_dir="$repo_root/cli/azd/extensions/microsoft.azd.extensions/internal/resources/languages"
proto_dir="$languages_dir/proto"
python_out="$languages_dir/python/generated_proto"
javascript_out="$languages_dir/javascript/generated/proto"

for required_dir in \
    "$proto_dir" \
    "$azd_dir/grpc/proto/azd/extensions/v1" \
    "$azd_dir/grpc/proto/azd/extensions/v1beta" \
    "$azd_dir/pkg/azdext"; do
    if [ ! -d "$required_dir" ]; then
        echo "required directory not found: $required_dir" >&2
        exit 1
    fi
done

mkdir -p "$python_out" "$javascript_out"
find "$python_out" -maxdepth 1 -type f \( -name '*_pb2.py' -o -name '*_pb2_grpc.py' \) -delete
find "$javascript_out" -maxdepth 1 -type f \( -name '*_pb.js' -o -name '*_grpc_pb.js' \) -delete

python_proto_include="$(python -c 'import grpc_tools, os; print(os.path.join(os.path.dirname(grpc_tools.__file__), "_proto"))')"

python -m grpc_tools.protoc \
    -I "$proto_dir" \
    -I "$python_proto_include" \
    --python_out="$python_out" \
    "$proto_dir"/*.proto

set --
for proto_file in "$proto_dir"/*.proto; do
    if grep -q '^service ' "$proto_file"; then
        set -- "$@" "$proto_file"
    fi
done

python -m grpc_tools.protoc \
    -I "$proto_dir" \
    -I "$python_proto_include" \
    --grpc_python_out="$python_out" \
    "$@"

protoc \
    -I "$proto_dir" \
    -I /usr/include \
    --plugin="protoc-gen-js=$(command -v protoc-gen-js)" \
    --js_out="import_style=commonjs,binary:$javascript_out" \
    "$proto_dir"/*.proto

set --
for proto_file in "$proto_dir"/*.proto; do
    if grep -q '^service ' "$proto_file"; then
        set -- "$@" "$proto_file"
    fi
done

grpc_tools_node_protoc \
    -I "$proto_dir" \
    -I /usr/include \
    --plugin="protoc-gen-grpc=$(command -v grpc_tools_node_protoc_plugin)" \
    --grpc_out="grpc_js:$javascript_out" \
    "$@"

make -C "$azd_dir" proto
