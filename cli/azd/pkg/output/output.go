package output

import (
	"io"
	"os"
	"strings"

	"github.com/mattn/go-colorable"
)

// Gets the default writer for print to the console
// Will respect NO_COLOR env var when specified with any value
func GetDefaultWriter() io.Writer {
	noColor := os.Getenv("NO_COLOR")
	if strings.TrimSpace(noColor) == "" {
		return colorable.NewColorableStdout()
	}

	return colorable.NewNonColorable(os.Stdout)
}
