// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package output

import (
	"fmt"

	"github.com/jmespath-community/go-jmespath/pkg/api"
)

// ApplyQuery applies a JMESPath query to the given data and returns the filtered result.
// If the query is empty, the original data is returned unchanged.
// If the query is invalid, an error with a hint to JMESPath documentation is returned.
func ApplyQuery(data interface{}, query string) (interface{}, error) {
	if query == "" {
		return data, nil
	}

	result, err := api.Search(query, data)
	if err != nil {
		return nil, fmt.Errorf(
			"invalid JMESPath query: %w. See https://jmespath.org for syntax help",
			err,
		)
	}

	return result, nil
}
