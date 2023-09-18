package templates

import (
	"fmt"
	"strings"
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
	// DevCenter use case
	// <devcenter>/<project>/<catalog>/<env-definition>
	case 3:
		return path, nil
	default:
		return "", fmt.Errorf(
			"template '%s' should either be <owner>/<repo> for GitHub repositories, "+
				"or <repo> for Azure-Samples GitHub repositories", path)
	}
}
