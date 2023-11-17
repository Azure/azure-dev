package runcontext

import (
	"log"
	"os"
	"strconv"
)

const cUseCloudShellAuthEnvVar = "AZD_IN_CLOUDSHELL"

func IsRunningInCloudShell() bool {
	if azdInCloudShell, has := os.LookupEnv(cUseCloudShellAuthEnvVar); has {
		if use, err := strconv.ParseBool(azdInCloudShell); err == nil && use {
			log.Printf("running in Cloud Shell")
			return true
		}
	}

	return false
}
