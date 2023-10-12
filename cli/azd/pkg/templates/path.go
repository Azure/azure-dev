package templates

import (
	"fmt"
	"log"
	"strings"

	"github.com/azure/azure-dev/cli/azd/pkg/output"
)

// Absolute returns an absolute template path, given a possibly relative template path. An absolute path also corresponds to
// a fully-qualified URI to a git repository.
//
// See Template.Path for more details.
func Absolute(path string) (string, error) {
	// already a git URI, return as-is
	if strings.HasPrefix(path, "git") || strings.HasPrefix(path, "http") {
		return path, nil
	}

	path = strings.TrimRight(path, "/")

	switch strings.Count(path, "/") {
	case 0:
		return fmt.Sprintf("https://github.com/Azure-Samples/%s", path), nil
	case 1:
		return fmt.Sprintf("https://github.com/%s", path), nil
	default:
		return "", fmt.Errorf(
			"template '%s' should either be <owner>/<repo> for GitHub repositories, "+
				"or <repo> for Azure-Samples GitHub repositories", path)
	}
}

// Hyperlink returns a hyperlink to the given template path.
// If the path is cannot be resolved absolutely, it is returned as-is.
func Hyperlink(path string) string {
	url, err := Absolute(path)
	if err != nil {
		log.Printf("error: getting absolute url from template: %v", err)
		return path
	}
	return output.WithHyperlink(url, path)
}
