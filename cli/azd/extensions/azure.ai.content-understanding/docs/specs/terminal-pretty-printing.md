# Content Understanding terminal pretty-printing

## Status

- **Component:** `cli/azd/extensions/azure.ai.content-understanding`
- **Spec status:** Proposed; pending experience review and approval.
- **Implementation:** [Azure/azure-dev#10784](https://github.com/Azure/azure-dev/pull/10784)
- **Contract review:** [New `--pretty` option](https://github.com/Azure/azure-dev/pull/10784#discussion_r4497328559)

## Goal and scope

Let people read an analysis result directly in the terminal without requiring a
separate Markdown viewer. Keep machine-readable and saved-result workflows
available through the existing output options.

This is an incremental experience spec for `azd ai cu analyze --pretty` and its
`-p` shorthand, not a specification of the entire Content Understanding CLI.
It also describes the analysis feedback accompanying this implementation.
Existing commands, input selectors, authentication, and service API selection are
outside the scope of this contract change.

## Command contract

| Surface | Contract |
| --- | --- |
| Command | Existing `azd ai cu analyze [INPUTS...]` |
| Option | `--pretty` |
| Shorthand | `-p`, equivalent to `--pretty` |
| Type and default | Boolean; `false` |
| Enable | `--pretty`, `--pretty=true`, `-p`, or `-p=true` |
| Disable | Omit the option, or specify `--pretty=false` or `-p=false` |
| Input cardinality | Exactly one resolved input when enabled |
| Result view | Streamed LLM-input text, using the default output format |

No commands, positional parameters, environment variables, or YAML schema fields
are added or removed.

Examples assume an endpoint and authentication are already configured:

```bash
# Default text, suitable for another viewer or saving through shell redirection.
azd ai cu analyze invoice.pdf > invoice.md

# Read a formatted preview directly in the terminal.
azd ai cu analyze invoice.pdf --pretty
azd ai cu analyze invoice.pdf -p

# Use an existing URL input selector.
azd ai cu analyze --url "https://example.com/invoice.pdf" -p

# Save a per-input outcome report alongside a streamed preview.
azd ai cu analyze invoice.pdf -p --report-file report.json
```

### Compatibility and validation

When pretty-printing is enabled:

- Require one resolved input, whether selected through positional input or an
  existing named input selector. Reject batches.
- Reject `--output json`, `--output none`, `--llm-input=false`, enabled `--usage`,
  enabled `--dry-run`, and explicitly supplied `--output-file` or `--output-dir`.
  Explicit empty saved-output values are not treated as omitted.
- Report the conflict and how to correct it before authentication, service
  requests, or output-file creation.
- Allow `--report-file`; its existing outcome-report contract is independent of
  the preview.
- Do not add a confirmation prompt. `--no-prompt` does not prevent pretty-printing.

When disabled, the existing analysis output options and validation apply.
The shorthand and long option address the same boolean setting.

## Preview experience

- Format Markdown headings, emphasis, lists, and supported tables for reading.
- Include document metadata with its `---` delimiters, page comments, and source
  line breaks. Resolve reference-style links across table boundaries.
- Display simple HTML tables with all columns. If a table is too wide, show
  labeled cards with a yellow layout notice and comment-style row labels such as
  `<!-- Row 1 -->`.
- For unsupported HTML tables, including merged cells or unsupported rich/empty
  cells, display a yellow notice and the table source.
- Use a consistent heading color, a distinct table color, and standard azd gray
  for metadata, comments, and generated row labels. Reserve yellow for layout
  notices.
- Honor `NO_COLOR`. A terminal preview uses the available terminal width;
  redirected or piped previews are uncolored and use an 80-column layout.

The option changes only the streamed presentation. It does not pretty-print
saved analysis results or reports. It does not fetch document links or images
and does not issue additional analysis requests.

### Content and terminal safety

Document URLs are content, not diagnostic identifiers. Keep their username/password component,
query parameters, fragments, and escaping rather than applying diagnostic URL
redaction. Rendering may decode character references once; it must not
double-decode ordinary content.

Remove terminal-control and bidirectional-control characters supplied by document
content, including controls encoded as character references with or without a
semicolon. Renderer-generated styling is allowed only when color is enabled.

## Analysis feedback

Analysis progress applies to ordinary analysis as well as pretty-printing:

- Show an animated, action-colored `Analyzing <target>...` status only when
  stdout is an interactive terminal and the output format is `default`.
- For concurrent analysis, share one status line that cycles through active
  targets. Do not display skipped inputs as active analyses.
- Display local file names. For URL targets, omit usernames/passwords, query parameters, and
  fragments, and remove terminal controls from displayed targets. Do not change
  the URL sent to the service.
- Do not show progress for dry runs, redirected or piped stdout, JSON output, or
  silent output. Do not write progress into saved results or reports.
- Stop and clear progress before final results or errors are displayed,
  including cancellation. Honor `NO_COLOR` and surface terminal-write failures.

Missing-endpoint guidance displays the literal example
`https://<your-resource>.services.ai.azure.com`, not a path-redaction marker.
Real diagnostic URLs remain subject to diagnostic redaction.

## Errors and limitations

- Analysis, rendering, cancellation, and output-write failures return nonzero.
  Do not silently substitute raw output after a preview-rendering failure.
- Long values or cards can exceed terminal width. Markdown tables may shorten
  long links.
- Unicode alignment, right-to-left text, and emoji appearance depend on the
  terminal and font.
- This contract does not promise automatic background-color detection or a
  specific RGB appearance across terminal themes.

## Acceptance criteria

| Scenario | Expected result |
| --- | --- |
| Omitted option or explicit false | Existing text/JSON/file behavior, as selected by other options |
| `--pretty` versus `-p`, including explicit true | Equivalent preview and validation |
| Pretty-printing with an incompatible option or batch | Actionable nonzero error before authentication, requests, or file writes |
| Pretty-printing with `--report-file` | Streamed preview plus the existing structured outcome report |
| Wide supported HTML table | Cards with all columns, comment-style row labels, and a layout notice |
| Unsupported HTML table | Notice and table source |
| `NO_COLOR` or redirected preview | No color sequences; redirected layout uses 80 columns |
| Analysis progress in an interactive terminal | Animated active-target feedback, removed before final output |
| Redirects, JSON/none, dry runs, or skipped inputs | No analysis progress for those cases |
| Credential-bearing URL used as a progress target | No username/password, query, or fragment in status; service request is unchanged |
| Ordinary document URLs and character references | Content is retained without diagnostic redaction or double decoding |
| Raw or encoded terminal/bidirectional controls | No document-supplied active controls in the final preview |
| Cancellation or writer failure | Progress stops, workers finish, and the command returns an error |

## Non-goals

- A GUI viewer or a separate preview command.
- Pretty-printing a batch or writing formatted previews through `--output-file`
  or `--output-dir`.
- Changing analyzer behavior, service requests, authentication, API versions,
  or saved report schemas.
- Adding telemetry instrumentation or new persisted settings.
- A complete Content Understanding CLI experience specification.
