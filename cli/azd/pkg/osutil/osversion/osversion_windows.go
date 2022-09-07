package osversion

import (
	"errors"
	"fmt"

	"golang.org/x/sys/windows"
)

func GetVersion() (string, error) {
	version := windows.RtlGetVersion()
	if version != nil {
		return fmt.Sprintf("%d.%d.%d", version.MajorVersion, version.MinorVersion, version.BuildNumber), nil
	}

	return "", errors.New("windows.RtlGetVersion returned nil")
}
