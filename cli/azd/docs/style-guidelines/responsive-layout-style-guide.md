# Azure Developer CLI (`azd`) Responsive Layout Style Guide

<!-- cspell:ignore tabwriter -->

## Overview

This guide defines the responsive list and table layouts used by Azure Developer CLI commands.

Primary list-command output — such as `azd tool list`, `azd tool check`, `azd extension list` (and its alias `azd ext list`), `azd extension source list`, `azd template list`, `azd template source list`, and `azd copilot consent list` — renders through the shared responsive formatter. 

The shared component is `output.PrettyTableFormatter`, configured via `output.PrettyTableFormatterOptions`.

- Formatter & breakpoint logic: [`pkg/output/pretty_table.go`](../../pkg/output/pretty_table.go)
- Column & option types: [`pkg/output/pretty_table_types.go`](../../pkg/output/pretty_table_types.go)
- Table rendering: [`pkg/output/pretty_table_table.go`](../../pkg/output/pretty_table_table.go)
- Stacked-row rendering: [`pkg/output/pretty_table_cards.go`](../../pkg/output/pretty_table_cards.go)
- Width/layout math: [`pkg/output/pretty_table_layout.go`](../../pkg/output/pretty_table_layout.go)

> Reference implementations: `azd tool list` in [`cmd/tool.go`](../../cmd/tool.go), `azd extension list` in [`cmd/extension.go`](../../cmd/extension.go). Model new list commands on these.

## Responsive Breakpoints

The formatter selects one of three layouts from the current terminal width (`resolveBreakpoint`). Defaults live in `pretty_table_types.go` and can be overridden per command via `FullThreshold` / `CompactThreshold`.

| Layout | Width condition | What renders |
| --- | --- | --- |
| **Full** | `width >= 100` (`DefaultFullThreshold`) | All columns |
| **Compact** | `width >= 60` (`DefaultCompactThreshold`) | Columns with `Priority <= 2`; dropped columns collapse into one header-only `···` marker |
| **Stacked rows** | `width < 60` | Each row becomes a vertical key/value block, optionally grouped into sections |

- Do **not** invent new width thresholds per command. Use the shared defaults unless there is a strong, documented reason to override them.

## Defining Columns

Each column is a `PrettyColumn` (a `Column` plus responsive metadata). Use the comments in [`pretty_table_types.go`](../../pkg/output/pretty_table_types.go) as the API reference. The following caller-facing conventions are not captured fully by those type comments:

- **Headings are UPPERCASE**.
- Truncation keeps at least 5 visible characters, and wrapping uses at most 2 lines.
- **`CardTitle`** selects the blue heading for each block in the stacked-row layout.
- **`CardGroupColumn`** must match a defined column heading. It groups stacked rows under section headers, preserves input order, and does **not** sort; sort rows before formatting.
- **`ResponsiveColumnHint: true`** adds the header-only `···` marker at the compact breakpoint when columns are dropped. The compact hint includes `Showing N of M columns...`; when only truncation occurs, including in the full layout, the hint contains only the resize/JSON guidance.


## Header, Spacing, and Status Styling

- **Header row**: bold high-white (`color.New(color.Bold, color.FgHiWhite)`, equivalent to `WithBold`), followed by a **gray** underline rule (`WithGrayFormat`).
- **Column spacing**: a fixed `columnPadding` (3 spaces). **No box-drawing separators** and **no `tabwriter`** in primary list-command output; detail screens and operation previews may use their established formatting patterns.
- **Empty values**: in the **table** layouts, render a literal `-` for a missing value (e.g. `INSTALLED` with no installed version). This is done in the column's `ValueTemplate` (e.g. `{{if .Version}}{{.Version}}{{else}}-{{end}}`), not automatically by the formatter. In the **stacked-row** layout, empty fields are omitted rather than shown as `-`.
- **Status colors**: reuse an existing helper only when it covers the command's complete status vocabulary; otherwise add a dedicated helper. Keep equivalent meanings consistent:

| Status text | Meaning | Color helper |
| --- | --- | --- |
| `Installed` / `Up to date` | Present and current | `WithSuccessFormat` |
| `Update available` | Installed but outdated | `WithWarningFormat` |
| `Not installed` | Absent (not an error) | `WithGrayFormat` |

## Layout Examples

**Full table** (`width >= 100`) — every column, gray underline rule:

```
ID                  NAME                           STATUS             INSTALLED   LATEST   SOURCE
────────────────────────────────────────────────────────────────────────────────────────────────────
az-cli              Azure CLI                      Up to date         1.0.0       1.0.0    azd
```

**Compact table** (`60 <= width < 100`) — only `Priority <= 2` columns; the dropped columns collapse into a single header-only `···` column (data rows leave it blank), with a hint line:

```
ID                  NAME                           STATUS             INSTALLED   ···
────────────────────────────────────────────────────────────────────────────────────────
az-cli              Azure CLI                      Up to date         1.0.0

Showing 4 of 6 columns. Resize the terminal or run with -o json for full details.
```

**Stacked-row layout** (`width < 60`) — each row becomes a vertical key/value block, grouped by `CardGroupColumn`, with the `CardTitle` value as the heading:

```
── SOURCE: azd ─────────────────────

Azure CLI
ID:         az-cli
STATUS:     Up to date
INSTALLED:  1.0.0
```

## List Command Guidelines

- **Always support `-o json`.** The responsive layouts are for humans; scripts and full detail should use `--output json`. The compact hint explicitly points users there.
