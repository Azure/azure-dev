package repository

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/azure/azure-dev/cli/azd/pkg/input"
)

// tabWrite transforms tabbed output into formatted strings with a given minimal padding.
// For more information, refer to the tabwriter package.
func tabWrite(selections []string, padding int) ([]string, error) {
	tabbed := strings.Builder{}
	tabW := tabwriter.NewWriter(&tabbed, 0, 0, padding, ' ', 0)
	_, err := tabW.Write([]byte(strings.Join(selections, "\n")))
	if err != nil {
		return nil, err
	}
	err = tabW.Flush()
	if err != nil {
		return nil, err
	}

	return strings.Split(tabbed.String(), "\n"), nil
}

// Prompts the user to input a valid directory.
func promptDir(
	ctx context.Context,
	console input.Console,
	message string) (string, error) {
	for {
		path, err := console.PromptFs(ctx, input.ConsoleOptions{
			Message: message,
		}, input.FsOptions{
			SuggestOpts: input.FsSuggestOptions{
				ExcludeFiles: true,
			},
		})
		if err != nil {
			return "", err
		}

		fs, err := os.Stat(path)
		if errors.Is(err, os.ErrNotExist) || fs != nil && !fs.IsDir() {
			console.Message(ctx, fmt.Sprintf("'%s' is not a valid directory", path))
			continue
		}

		if err != nil {
			return "", err
		}

		path, err = filepath.Abs(path)
		if err != nil {
			return "", err
		}

		return path, err
	}
}

// relSafe attempts to return a relative path from root to path. If unsuccessful, the path is returned as-is.
func relSafe(root string, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil {
		return path
	}

	return rel
}
