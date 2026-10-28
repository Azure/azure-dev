# Environment variable substitution in Bicep parameter files

`azd` resolves `${VAR}` placeholders in the `value` of each parameter in `main.parameters.json` (and
`<module>.parameters.json`) using the active azd environment, falling back to the process environment. Default values
(`${VAR=default}`) are supported.

Substitution runs on the parsed parameter values, not on the raw JSON text. A replacement is always inserted as
literal text, so quotes, backslashes, and newlines in a variable can never break or change the structure of the
parameters. There are three cases.

## 1. Entire value: the variable holds a complete JSON value

For a parameter declared as `object` or `array` (including typed arrays such as `string[]`), a `value` that is a single
string is resolved and then parsed as JSON. Use this when the variable holds the whole object or array.

```bash
azd env set MY_CONFIG '{"name":"app","ports":[80,443],"note":"say \"hi\""}'
azd env set REGIONS '["eastus","westus"]'
```

```json
{
  "parameters": {
    "config": { "value": "${MY_CONFIG}" },
    "regions": { "value": "${REGIONS}" }
  }
}
```

The variable must contain valid JSON, otherwise `azd` fails with an error naming the parameter. A parameter mapped to
an unset variable is omitted.

## 2. Internal placeholders: literal text inside strings

When an object or array is written in the parameters file, placeholders inside its strings (including nested objects and
arrays) are replaced with the variable's text as is.

```bash
azd env set MESSAGE 'He said "hi"
on two lines, path C:\temp'
azd env set FIRST one
azd env set SECOND two
```

```json
{
  "parameters": {
    "settings": { "value": { "message": "${MESSAGE}", "nested": { "items": ["${FIRST}", "${SECOND}"] } } },
    "names": { "value": ["${FIRST}", "prefix-${SECOND}"] },
    "title": { "value": "${MESSAGE}" }
  }
}
```

The value of `message` is exactly the text of `MESSAGE`. If a variable contains JSON, such as `{"a":1}`, it stays a
string in this case; it is not parsed into an object. Use case 1 for that.

## 3. Values authored in the parameters file

Objects, arrays, numbers, booleans, and `null` written directly in the parameters file are passed through unchanged, with
their structure and types. Only strings are searched for placeholders.

```json
{
  "parameters": {
    "sku": { "value": { "name": "Standard", "capacity": 2, "zoneRedundant": false } },
    "ports": { "value": [80, 443] }
  }
}
```

## Notes

- Replacement text is not substituted a second time, so a variable whose value contains `${OTHER}` keeps that text.
- Values must not be JSON-escaped when set with `azd env set`. `azd pipeline config` escapes values (quotes and
  backslashes) before storing them in the CI system. Because replacement text is literal, a value read back from the CI
  environment is used exactly as stored.
