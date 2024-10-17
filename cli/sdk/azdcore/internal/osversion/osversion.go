//go:build !windows && !linux && !darwin

package osversion

import "errors"

func GetVersion() (string, error) {
	return "", errors.New("unsupported OS")
}
