package runcontext

import (
	"log"
	"os"
	"strconv"
)

// AzdInCloudShellEnvVar is the environment variable that is set when running in Cloud Shell. It is set to a value recognized
// by strconv.ParseBool.
//
// Use [IsRunningInCloudShell] to check if the current process is running in Cloud Shell.
const AzdInCloudShellEnvVar = "AZD_IN_CLOUDSHELL"

func IsRunningInCloudShell() bool {
	if azdInCloudShell, has := os.LookupEnv(AzdInCloudShellEnvVar); has {
		if use, err := strconv.ParseBool(azdInCloudShell); err == nil && use {
			log.Printf("running in Cloud Shell")
			return true
		}
	}

	return false
}
