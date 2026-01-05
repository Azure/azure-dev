package utils

func IsLocalFilePath(fileID string) bool {
	if fileID == "" {
		return false
	}
	if len(fileID) > 6 && fileID[:6] == "local:" {
		return true
	}
	return false
}

func GetLocalFilePath(fileID string) string {
	if IsLocalFilePath(fileID) {
		return fileID[6:]
	}
	return fileID
}
