package exec

import (
	"regexp"
	"strings"
)

type redactData struct {
	matchString   *regexp.Regexp
	replaceString string
}

// redactedReplacement is the string that will replace sensitive data in the output.
const redactedReplacement = "<redacted>"

func RedactSensitiveArgs(args []string, sensitiveDataMatch []string) []string {
	if len(sensitiveDataMatch) == 0 {
		return args
	}
	redactedArgs := make([]string, len(args))
	for i, arg := range args {
		redacted := arg
		for _, sensitiveData := range sensitiveDataMatch {
			redacted = strings.ReplaceAll(redacted, sensitiveData, redactedReplacement)
		}
		redactedArgs[i] = redacted
	}
	return redactedArgs
}

func RedactSensitiveData(msg string) string {
	var regexpRedactRules = map[string]redactData{
		"access token": {
			regexp.MustCompile("\"accessToken\": \".*\""),
			"\"accessToken\": \"" + redactedReplacement + "\"",
		},
		"deployment token": {
			regexp.MustCompile(`--deployment-token \S+`),
			"--deployment-token " + redactedReplacement,
		},
		"username": {
			regexp.MustCompile(`--username \S+`),
			"--username " + redactedReplacement,
		},
		"password": {
			regexp.MustCompile(`--password \S+`),
			"--password " + redactedReplacement,
		},
		"kubectl-from-literal": {
			regexp.MustCompile(`--from-literal=([^=]+)=(\S+)`),
			"--from-literal=$1=" + redactedReplacement,
		},
		"combined-arg": {
			regexp.MustCompile(`(.*)=(\S+)`),
			"$1=" + redactedReplacement,
		},
	}

	for _, redactRule := range regexpRedactRules {
		regMatchString := redactRule.matchString
		msg = regMatchString.ReplaceAllString(msg, redactRule.replaceString)
	}
	return msg
}
