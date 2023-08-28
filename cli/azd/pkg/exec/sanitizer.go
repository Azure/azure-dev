package exec

import (
	"regexp"
	"strings"
)

type redactData struct {
	matchString   *regexp.Regexp
	replaceString string
}

const cRedacted = "<redacted>"

func RedactSensitiveArgs(args []string, sensitiveDataMatch []string) []string {
	if len(sensitiveDataMatch) == 0 {
		return args
	}
	redactedArgs := make([]string, len(args))
	for i, arg := range args {
		redacted := arg
		for _, sensitiveData := range sensitiveDataMatch {
			redacted = strings.ReplaceAll(redacted, sensitiveData, cRedacted)
		}
		redactedArgs[i] = redacted
	}
	return redactedArgs
}

func RedactSensitiveData(msg string) string {
	var regexpRedactRules = map[string]redactData{
		"access token": {
			regexp.MustCompile("\"accessToken\": \".*\""),
			"\"accessToken\": \"" + cRedacted + "\"",
		},
		"deployment token": {
			regexp.MustCompile(`--deployment-token \S+`),
			"--deployment-token " + cRedacted,
		},
		"username": {
			regexp.MustCompile(`--username \S+`),
			"--username " + cRedacted,
		},
		"password": {
			regexp.MustCompile(`--password \S+`),
			"--password " + cRedacted,
		},
		"kubectl-from-literal": {
			regexp.MustCompile(`--from-literal=([^=]+)=(\S+)`),
			"--from-literal=$1=" + cRedacted,
		},
		"combined-arg": {
			regexp.MustCompile(`(.*)=(\S+)`),
			"$1=" + cRedacted,
		},
	}

	for _, redactRule := range regexpRedactRules {
		regMatchString := redactRule.matchString
		msg = regMatchString.ReplaceAllString(msg, redactRule.replaceString)
	}
	return msg
}
