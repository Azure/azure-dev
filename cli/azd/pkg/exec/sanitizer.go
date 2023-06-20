package exec

import (
	"bytes"
	"fmt"
	"io"
	"regexp"
	"strings"
)

type redactData struct {
	matchString   *regexp.Regexp
	replaceString string
}

const cRedacted = "<redacted>"

var regexpRedactRules map[string]redactData

func init() {
	regexpRedactRules = map[string]redactData{
		"access token": {
			regexp.MustCompile(`"accessToken":(\s*)".*"`),
			`"accessToken":$1"` + cRedacted + `"`,
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
}

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
	for _, redactRule := range regexpRedactRules {
		regMatchString := redactRule.matchString
		msg = regMatchString.ReplaceAllString(msg, redactRule.replaceString)
	}
	return msg
}

type sanitizingLogWriter struct {
	w io.Writer
}

func (w *sanitizingLogWriter) Write(p []byte) (int, error) {
	var written int
	var err error
	for {
		var line string
		var atEOF bool
		if i := bytes.IndexByte(p, '\n'); i >= 0 {
			written += i + 1
			line = string(bytes.TrimFunc(p[0:i], func(r rune) bool {
				return r == '\r'
			}))
			p = p[i+1:]
		} else {
			written += len(p)
			line = string(p)
			atEOF = true
		}
		if len(line) > 0 {
			line = RedactSensitiveData(line)
		}
		_, err = fmt.Fprintf(w.w, "   %s\n", line)
		if err != nil {
			break
		}
		if atEOF {
			break
		}
	}
	return written, err
}
