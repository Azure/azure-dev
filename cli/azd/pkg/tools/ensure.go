package tools

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log"
	osexec "os/exec"
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

	confirmedTools := make(map[string]struct{})
	if fromCtx, ok := ctx.Value(installedCheckCacheKey).(map[string]struct{}); ok && fromCtx != nil {
		confirmedTools = fromCtx
	}

	for _, tool := range tools {
		_, ok := confirmedTools[tool.Name()]
		if ok {
			log.Printf("Skipping install check for '%s'. It was previously confirmed.", tool.Name())
			continue
		}

		err := tool.CheckInstalled(ctx)
		var errSem *ErrSemver
		if errors.As(err, &errSem) {
			errorMsg := err.Error()
			if _, hasV := errorsEncountered[errorMsg]; !hasV {
				allErrors = append(allErrors, err)
				errorsEncountered[errorMsg] = struct{}{}
			}
		} else if errors.Is(err, osexec.ErrNotFound) {
			allErrors = append(
				allErrors, fmt.Errorf("%s is not installed, see %s to install", tool.Name(), tool.InstallUrl()))

		} else if err != nil {
			errorMsg := err.Error()
			if _, hasV := errorsEncountered[errorMsg]; !hasV {
				allErrors = append(allErrors, fmt.Errorf("error checking for external tool %s: %w", tool.Name(), err))
				errorsEncountered[errorMsg] = struct{}{}
			}
		}

		// Mark the current tool as confirmed
		confirmedTools[tool.Name()] = struct{}{}
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

type confirmCacheKey string

const (
	installedCheckCacheKey confirmCacheKey = "checkCache"
)

func WithInstalledCheckCache(ctx context.Context) context.Context {
	return context.WithValue(ctx, installedCheckCacheKey, make(map[string]struct{}))
}
