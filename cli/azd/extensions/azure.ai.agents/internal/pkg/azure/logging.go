package azure

import "regexp"

var ConnectionStringJSONRegex = regexp.MustCompile(`("[\w]*(?:CONNECTION_STRING|ConnectionString)":\s*)"[^"]*"`)
