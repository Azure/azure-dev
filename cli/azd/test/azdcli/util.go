package azdcli

// GetOutputFlagValue returns the value specified by --output.
// If --output is not specified, it returns an empty string
func GetOutputFlagValue(args []string) string {
	for i, arg := range args {
		if arg == "--output" {
			return args[i+1]
		}
	}

	return ""
}
