package osversion

import (
	"bytes"
	"fmt"
	"os/exec"
	"strings"
)

func GetVersion() (string, error) {
	swVersCmd := exec.Command("sw_vers", "--productVersion")
	outputBytes, err := swVersCmd.Output()

	if err != nil {
		return "", err
	}

	output := string(bytes.TrimSpace(outputBytes))

	fmt.Printf("count = %d\n", strings.Count(output, "."))

	if strings.Count(output, ".") == 1 {
		// they're not including the patch version, we'll tack it on for compatibility
		return output + ".0", nil
	}

	return output, err
}
