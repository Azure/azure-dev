// Copyright (c) Microsoft Corporation. All rights reserved.
// Licensed under the MIT License.

package telemetry

import (
	"crypto/sha256"
	"encoding/hex"
	"log"
	"net"
	"os"
	"path/filepath"

	"github.com/azure/azure-dev/cli/azd/pkg/config"
	"github.com/azure/azure-dev/cli/azd/pkg/osutil"
	"github.com/google/uuid"
)

const machineIdCacheFileName = "machine-id.cache"

var invalidMacAddresses = map[string]struct{}{
	"00:00:00:00:00:00": {},
	"ff:ff:ff:ff:ff:ff": {},
	"ac:de:48:00:11:22": {},
}

func sha256Hash(val string) string {
	sha := sha256.Sum256([]byte(val))
	hash := hex.EncodeToString(sha[:])
	return hash
}

// getMachineId returns a unique ID for the machine.
func getMachineId() string {
	// We store the machine ID on the filesystem not due to performance,
	// but to increase the stability of the ID constant across factors like changing mac addresses, NICs.
	return loadOrCalculate(calculateMachineId, machineIdCacheFileName)
}

func calculateMachineId() string {
	mac, ok := getMacAddress()

	if ok {
		return sha256Hash(mac)
	} else {
		// No valid mac address, return a GUID instead.
		return uuid.NewString()
	}
}

func loadOrCalculate(valueFunc func() string, cacheFileName string) string {
	configDir, err := config.GetUserConfigDir()
	cacheFile := filepath.Join(configDir, cacheFileName)

	if err != nil {
		log.Printf("could not load machineId from cache, returning default: %s", err)
		return valueFunc()
	}

	bytes, err := os.ReadFile(configDir)
	if err == nil {
		return string(bytes)
	}

	err = os.WriteFile(cacheFile, []byte(valueFunc()), osutil.PermissionFile)
	if err != nil {
		log.Printf("could not write machineId to cache, returning default: %s", err)
	}

	return valueFunc()
}

func getMacAddress() (string, bool) {
	interfaces, _ := net.Interfaces()
	for _, ift := range interfaces {
		if len(ift.HardwareAddr) > 0 && ift.Flags&net.FlagLoopback == 0 {
			hwAddr, err := net.ParseMAC(ift.HardwareAddr.String())
			if err != nil {
				continue
			}

			mac := hwAddr.String()
			if isValidMacAddress(mac) {
				return mac, true
			}
		}
	}

	return "", false
}

func isValidMacAddress(addr string) bool {
	_, invalidAddr := invalidMacAddresses[addr]
	return !invalidAddr
}
