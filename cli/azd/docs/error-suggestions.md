# Error Suggestions

Azure Developer CLI includes a pattern-based error suggestion system that provides user-friendly messaging for common errors. When users encounter well-known errors (quota limits, authentication failures, missing tools, etc.), azd displays:

1. A **user-friendly message** explaining what went wrong
2. An **actionable suggestion** for next steps
3. A **documentation link** for more information
4. The **original error** (in grey) for technical reference

## How It Works

```
┌─────────────────────────────────────────────────────────────────┐
│                        Error Occurs                             │
└─────────────────────────────────────────────────────────────────┘
                              │
                              ▼
┌─────────────────────────────────────────────────────────────────┐
│  ErrorMiddleware checks error message against patterns in       │
│  resources/error_suggestions.yaml                               │
└─────────────────────────────────────────────────────────────────┘
                              │
              ┌───────────────┴───────────────┐
              │                               │
              ▼                               ▼
┌─────────────────────────┐     ┌─────────────────────────────────┐
│   Pattern Matched       │     │   No Pattern Match              │
│   Wrap error with       │     │   Return original error         │
│   ErrorWithSuggestion   │     │   (may go to AI if enabled)     │
└─────────────────────────┘     └─────────────────────────────────┘
              │
              ▼
┌─────────────────────────────────────────────────────────────────┐
│  UxMiddleware displays:                                         │
│  1. User-friendly message (ERROR: ...)                          │
│  2. Actionable suggestion (Suggestion: ...)                     │
│  3. Documentation link (Learn more: ...)                        │
│  4. Original error in grey (technical details)                  │
└─────────────────────────────────────────────────────────────────┘
```

### Example Output

When a user hits a quota error, instead of seeing a wall of red text, they see:

```
ERROR: Your Azure subscription has reached a resource quota limit.

Suggestion: Request a quota increase through the Azure portal, or try deploying to a different region.
Learn more: https://learn.microsoft.com/azure/quotas/quickstart-increase-quota-portal

Deployment failed: QuotaExceeded for resource type Microsoft.Compute/virtualMachines in location eastus...
```

The original error is shown in grey at the bottom for users who need the technical details.

## Adding New Error Patterns

The error patterns are defined in [`resources/error_suggestions.yaml`](../resources/error_suggestions.yaml). This file is designed to be easily editable by anyone—no Go programming knowledge required.

### Basic Structure

```yaml
rules:
  - patterns:
      - "error text to match"
      - "another error text"
    message: "User-friendly explanation of what went wrong."
    suggestion: "Actionable next steps to fix the issue."
    docUrl: "https://learn.microsoft.com/..."  # optional
```

### Fields

| Field | Required | Description |
|-------|----------|-------------|
| `patterns` | Yes | List of strings to match against error messages |
| `message` | Yes | User-friendly explanation of what went wrong |
| `suggestion` | Yes | Actionable next steps for the user |
| `docUrl` | No | Link to relevant documentation |

### Pattern Types

#### 1. Simple Substring Matching (Default)

The simplest pattern type. Matches if the error message contains the pattern text anywhere. **Matching is case-insensitive**.

```yaml
- patterns:
    - "quota exceeded"        # Matches "QuotaExceeded", "QUOTA EXCEEDED", etc.
    - "QuotaExceeded"         # Also matches case-insensitively
  message: "Your Azure subscription has reached a resource quota limit."
  suggestion: "Request a quota increase through the Azure portal."
```

#### 2. Regular Expression Patterns

For more complex matching, prefix the pattern with `regex:`. This enables full regular expression support.

```yaml
- patterns:
    - "regex:(?i)authorization.*failed"    # (?i) = case-insensitive flag
    - "regex:BCP\\d{3}"                    # Matches BCP001, BCP123, etc.
  message: "You don't have permission to perform this operation."
  suggestion: "Check your Azure RBAC role assignments."
```

**Common regex patterns:**

| Pattern | Meaning |
|---------|---------|
| `(?i)` | Case-insensitive matching |
| `.*` | Match any characters |
| `\\d+` | Match one or more digits |
| `\\d{3}` | Match exactly 3 digits |
| `(foo\|bar)` | Match "foo" or "bar" |
| `\\s+` | Match whitespace |

**Note:** In YAML, backslashes must be escaped as `\\`.

### Rule Evaluation

- **First match wins**: Rules are evaluated in order from top to bottom
- **Order matters**: Place more specific patterns before general ones
- **Multiple patterns per rule**: If any pattern in a rule matches, that rule wins

### Best Practices

1. **Keep messages simple**: The message explains what went wrong in plain language

   ```yaml
   # ❌ Too technical
   message: "QuotaExceeded error for Microsoft.Compute/virtualMachines"
   
   # ✅ User-friendly
   message: "Your Azure subscription has reached a resource quota limit."
   ```

2. **Make suggestions actionable**: Tell users exactly what to do

   ```yaml
   # ❌ Vague
   suggestion: "Fix the authentication issue."
   
   # ✅ Actionable
   suggestion: "Run 'azd auth login' to sign in again."
   ```

3. **Include documentation links**: Help users learn more

   ```yaml
   docUrl: "https://learn.microsoft.com/azure/developer/azure-developer-cli/reference#azd-auth-login"
   ```

4. **Group related patterns**: Combine patterns that should have the same response

   ```yaml
   - patterns:
       - "AADSTS"                            # Azure AD error codes
       - "regex:(?i)authentication.*failed"
       - "regex:(?i)invalid.*credentials"
     message: "Authentication with Azure failed."
     suggestion: "Run 'azd auth login' to sign in again."
   ```

5. **Test your patterns**: Use the unit tests to verify patterns work correctly

   ```go
   // In pkg/errorhandler/matcher_test.go
   func TestMyNewPattern(t *testing.T) {
       service := NewErrorSuggestionService()
       result := service.FindSuggestion("your error message here")
       assert.NotNil(t, result)
       assert.NotEmpty(t, result.Message)
       assert.NotEmpty(t, result.Suggestion)
   }
   ```

## File Location

| File | Purpose |
|------|---------|
| `resources/error_suggestions.yaml` | Error patterns and suggestions (edit this!) |
| `pkg/errorhandler/types.go` | Go types for the YAML structure |
| `pkg/errorhandler/matcher.go` | Pattern matching engine |
| `pkg/errorhandler/service.go` | Service that loads and matches patterns |
| `pkg/output/ux/error_with_suggestion.go` | UX component for displaying errors |

## Complete Example

Here's an example of adding a new error pattern for a hypothetical "disk space" error:

```yaml
# Add this to resources/error_suggestions.yaml

  # ============================================================================
  # Disk Space Errors
  # ============================================================================
  - patterns:
      - "no space left on device"
      - "disk quota exceeded"
      - "regex:(?i)insufficient.*disk.*space"
      - "ENOSPC"
    message: "Your disk is full."
    suggestion: "Free up space by removing unused Docker images ('docker system prune') or clearing temporary files."
    docUrl: "https://docs.docker.com/config/pruning/"
```

After adding the pattern:

1. Run `go build` to ensure the YAML is valid
2. Run `go test ./pkg/errorhandler/...` to verify patterns load correctly
3. Test with a real error scenario if possible

## Architecture Notes

- **Embedded resource**: The YAML file is embedded into the azd binary at build time using Go's `//go:embed` directive
- **Lazy loading**: Patterns are loaded once on first use and cached
- **Regex caching**: Compiled regular expressions are cached for performance
- **No external dependencies**: Pattern matching works offline with no network calls
