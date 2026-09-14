// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package agent_yaml

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

func serializeMetadataTags(value any) (string, error) {
	var encoded string
	var tags []string
	switch value := value.(type) {
	case string:
		encoded = value
	case []string:
		tags = slices.Clone(value)
	case []any:
		tags = make([]string, len(value))
		for i, item := range value {
			tag, ok := item.(string)
			if !ok {
				return "", fmt.Errorf("metadata.tags[%d] must be a string", i)
			}
			tags[i] = tag
		}
	default:
		return "", fmt.Errorf("metadata.tags must be a string or a list of strings")
	}

	if len(tags) > 0 {
		for i, tag := range tags {
			if strings.TrimSpace(tag) == "" {
				return "", fmt.Errorf("metadata.tags[%d] must not be empty or whitespace", i)
			}
		}
		slices.Sort(tags)
		tags = slices.Compact(tags)
		// Foundry metadata values are strings. JSON preserves commas and quotes
		// within tags, unlike the legacy comma-separated authors representation.
		data, err := json.Marshal(tags)
		if err != nil {
			return "", fmt.Errorf("serialize metadata.tags: %w", err)
		}
		encoded = string(data)
	}
	if utf8.RuneCountInString(encoded) > 512 {
		return "", fmt.Errorf("metadata.tags exceeds the Foundry limit of 512 characters after serialization")
	}
	return encoded, nil
}
