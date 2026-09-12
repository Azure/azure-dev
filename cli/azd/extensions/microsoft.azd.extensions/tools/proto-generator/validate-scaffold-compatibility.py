#!/usr/bin/env python3

import sys

from google.protobuf import descriptor_pb2


def load_descriptor_set(path):
    descriptor_set = descriptor_pb2.FileDescriptorSet()
    with open(path, "rb") as descriptor_file:
        descriptor_set.ParseFromString(descriptor_file.read())
    return descriptor_set


def collect_symbols(descriptor_set):
    messages = {}
    enums = {}
    services = {}

    def collect_message(package, parents, message):
        name = ".".join(filter(None, [package, *parents, message.name]))
        messages[name] = message
        for enum in message.enum_type:
            enums[f"{name}.{enum.name}"] = enum
        for nested in message.nested_type:
            collect_message(package, [*parents, message.name], nested)

    for file_descriptor in descriptor_set.file:
        for message in file_descriptor.message_type:
            collect_message(file_descriptor.package, [], message)
        for enum in file_descriptor.enum_type:
            enums[".".join(filter(None, [file_descriptor.package, enum.name]))] = enum
        for service in file_descriptor.service:
            services[".".join(filter(None, [file_descriptor.package, service.name]))] = service

    return messages, enums, services


def oneof_name(message, field):
    if not field.HasField("oneof_index"):
        return ""
    return message.oneof_decl[field.oneof_index].name


def validate_messages(subset, canonical):
    for name, subset_message in sorted(subset.items()):
        canonical_message = canonical.get(name)
        if canonical_message is None:
            raise ValueError(f"scaffold message {name} is missing from canonical v1")
        if subset_message.options.map_entry != canonical_message.options.map_entry:
            raise ValueError(f"{name} map-entry shape differs from canonical v1")

        canonical_fields = {field.number: field for field in canonical_message.field}
        for subset_field in subset_message.field:
            canonical_field = canonical_fields.get(subset_field.number)
            field_name = f"{name}.{subset_field.name}"
            if canonical_field is None:
                raise ValueError(
                    f"{field_name} field number {subset_field.number} is missing from canonical v1"
                )
            if subset_field.name != canonical_field.name:
                raise ValueError(
                    f"{field_name} field number {subset_field.number} is named "
                    f"{canonical_field.name} in canonical v1"
                )
            if subset_field.type != canonical_field.type:
                raise ValueError(f"{field_name} type differs from canonical v1")
            if subset_field.label != canonical_field.label:
                raise ValueError(f"{field_name} cardinality differs from canonical v1")
            if subset_field.type_name != canonical_field.type_name:
                raise ValueError(f"{field_name} referenced type differs from canonical v1")
            if subset_field.proto3_optional != canonical_field.proto3_optional:
                raise ValueError(f"{field_name} optional presence differs from canonical v1")
            if oneof_name(subset_message, subset_field) != oneof_name(
                canonical_message, canonical_field
            ):
                raise ValueError(f"{field_name} oneof membership differs from canonical v1")


def validate_enums(subset, canonical):
    for name, subset_enum in sorted(subset.items()):
        canonical_enum = canonical.get(name)
        if canonical_enum is None:
            raise ValueError(f"scaffold enum {name} is missing from canonical v1")

        canonical_values = {value.name: value.number for value in canonical_enum.value}
        for subset_value in subset_enum.value:
            if canonical_values.get(subset_value.name) != subset_value.number:
                raise ValueError(
                    f"{name}.{subset_value.name} value differs from canonical v1"
                )


def validate_services(subset, canonical):
    for name, subset_service in sorted(subset.items()):
        canonical_service = canonical.get(name)
        if canonical_service is None:
            raise ValueError(f"scaffold service {name} is missing from canonical v1")

        canonical_methods = {
            method.name: method for method in canonical_service.method
        }
        for subset_method in subset_service.method:
            canonical_method = canonical_methods.get(subset_method.name)
            method_name = f"{name}.{subset_method.name}"
            if canonical_method is None:
                raise ValueError(f"scaffold method {method_name} is missing from canonical v1")
            if (
                subset_method.input_type != canonical_method.input_type
                or subset_method.output_type != canonical_method.output_type
            ):
                raise ValueError(
                    f"{method_name} request or response type differs from canonical v1"
                )
            if (
                subset_method.client_streaming != canonical_method.client_streaming
                or subset_method.server_streaming != canonical_method.server_streaming
            ):
                raise ValueError(f"{method_name} stream shape differs from canonical v1")


def main():
    if len(sys.argv) != 3:
        print(
            "usage: validate-scaffold-compatibility.py "
            "<scaffold-descriptors> <canonical-descriptors>",
            file=sys.stderr,
        )
        return 2

    scaffold_symbols = collect_symbols(load_descriptor_set(sys.argv[1]))
    canonical_symbols = collect_symbols(load_descriptor_set(sys.argv[2]))

    validate_messages(scaffold_symbols[0], canonical_symbols[0])
    validate_enums(scaffold_symbols[1], canonical_symbols[1])
    validate_services(scaffold_symbols[2], canonical_symbols[2])
    print("Stable scaffold protobuf contracts are compatible with canonical v1.")
    return 0


if __name__ == "__main__":
    try:
        sys.exit(main())
    except ValueError as error:
        print(f"scaffold protobuf compatibility failed: {error}", file=sys.stderr)
        sys.exit(1)
