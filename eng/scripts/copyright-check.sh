#!/bin/sh

HEADER1="// Copyright (c) Microsoft Corporation. All rights reserved."
HEADER2="// Licensed under the MIT License."

check_header() {
    local file=$1
    if ! head -n 1 "$file" | grep -q "$HEADER1"; then
        echo "Missing or incorrect first line of header in $file"
        return 1
    fi
    if ! head -n 2 "$file" | tail -n 1 | grep -q "$HEADER2"; then
        echo "Missing or incorrect second line of header in $file"
        return 1
    fi
    return 0
}

insert_header() {
    local file=$1
    echo "Inserting header in $file"
    (echo "$HEADER1"; echo "$HEADER2"; echo ""; cat "$file") > "$file.tmp" && mv "$file.tmp" "$file"
}

validate_headers() {
    local directory=$1
    local insert_missing=$2
    local all_files_valid=0
    for file in $(find "$directory" -name '*.go'); do
        if ! check_header "$file"; then
            all_files_valid=1
            if [ "$insert_missing" = true ]; then
                insert_header "$file"
            fi
        fi
    done
    return $all_files_valid
}

if [ "$#" -lt 1 ] || [ "$#" -gt 2 ]; then
    echo "Usage: $0 <directory> [--fix]"
    exit 1
fi

DIRECTORY=$1
INSERT_MISSING=false

if [ "$#" -eq 2 ] && [ "$2" = "--fix" ]; then
    INSERT_MISSING=true
fi

validate_headers "$DIRECTORY" "$INSERT_MISSING"