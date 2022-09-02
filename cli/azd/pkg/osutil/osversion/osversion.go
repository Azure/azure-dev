//go:build !windows && !linux && !darwin

package osversion

func GetVersion() string {
	return ""
}
