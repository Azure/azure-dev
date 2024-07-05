package middleware

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/azure/azure-dev/cli/azd/cmd/actions"
	"github.com/azure/azure-dev/cli/azd/internal"
	"github.com/jmespath/go-jmespath"
)

type QueryMiddleware struct {
	global *internal.GlobalCommandOptions
}

func (m *QueryMiddleware) Run(ctx context.Context, next NextFn) (*actions.ActionResult, error) {
	// Execute the next middleware or action in the chain
	fmt.Println(m.global.Query)
	result, err := next(ctx)
	if err != nil {
		return nil, err
	}

	if m.global.Query == "" {
		return result, nil
	}

	// Convert the result to JSON
	resultJSON, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("marshalling action result: %w", err)
	}

	// Apply the JMESPath query
	var jsonResult interface{}
	err = json.Unmarshal(resultJSON, &jsonResult)
	if err != nil {
		return nil, fmt.Errorf("unmarshalling action result: %w", err)
	}

	filteredResult, err := jmespath.Search(m.global.Query, jsonResult)
	if err != nil {
		return nil, fmt.Errorf("applying JMESPath query: %w", err)
	}

	// Convert the filtered result back to an ActionResult
	filteredResultJSON, err := json.Marshal(filteredResult)
	if err != nil {
		return nil, fmt.Errorf("marshalling filtered result: %w", err)
	}

	var actionResult actions.ActionResult
	err = json.Unmarshal(filteredResultJSON, &actionResult)
	if err != nil {
		return nil, fmt.Errorf("unmarshalling filtered result: %w", err)
	}

	return &actionResult, nil
}

// NewQueryMiddleware is the registration function for the QueryMiddleware
func NewQueryMiddleware(global *internal.GlobalCommandOptions) Middleware {
	return &QueryMiddleware{global: global}
}
