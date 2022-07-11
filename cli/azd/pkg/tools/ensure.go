package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
)

// missingToolErrors wraps a set of errors discovered when
// probing for tools and implements the Error interface to pretty
// print the underlying errors. We use this instead of the existing
// `multierr` package we use elsewhere, because we want to control
// the error string (the default one produced by multierr is not
// as nice as what we do here).
type missingToolErrors struct {
	errs []error
}

func (m *missingToolErrors) Error() string {
	buf := bytes.Buffer{}

	fmt.Fprintf(&buf, "required external tools are missing:")
	for _, err := range m.errs {
		fmt.Fprintf(&buf, "\n - %s", err.Error())
	}

	return buf.String()
}

// EnsureInstalled checks that all tools are installed, returning an
// error if one or more tools are not.
func EnsureInstalled(ctx context.Context, tools ...ExternalTool) error {
	var allErrors []error
	errorsEncountered := map[string]struct{}{}

	for _, tool := range tools {
		has, err := tool.CheckInstalled(ctx)
		var errSem *ErrSemver
		errorMsg := err.Error()
		if _, hasV := errorsEncountered[errorMsg]; !hasV {
			if errors.As(err, &errSem) {
				allErrors = append(allErrors, err)
			} else if err != nil {
				allErrors = append(allErrors, fmt.Errorf("error checking for external tool %s: %w", tool.Name(), err))
			} else if !has {
				allErrors = append(allErrors, fmt.Errorf("%s is not installed, please see %s to install", tool.Name(), tool.InstallUrl()))
			}
			errorsEncountered[errorMsg] = struct{}{}
		}
	}

	if len(allErrors) > 0 {
		return &missingToolErrors{errs: allErrors}
	}

	return nil
}

// Unique filters a slice of tools such that a tool with a
// given name only appears once.
func Unique(tools []ExternalTool) []ExternalTool {
	uniqueToolsMap := make(map[string]struct{})
	var uniqueTools []ExternalTool

	for _, tool := range tools {
		name := tool.Name()

		if _, has := uniqueToolsMap[name]; !has {
			uniqueToolsMap[name] = struct{}{}
			uniqueTools = append(uniqueTools, tool)
		}
	}

	return uniqueTools
}
