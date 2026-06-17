# Findings

Self-reflection triage, voice rules, and output format for the review.

## Self-Reflection Pass

Before emitting findings, run a quality filter. The goal is high signal-to-noise — the author should want to act on most findings.

For each finding, evaluate:

1. **Is this actionable?** Does it suggest a specific change or raise a specific concern? Vague observations are not actionable.
2. **Is this correct?** Does the finding accurately reflect what the code does? Cross-check against the actual diff — findings based on misreading the code should be dismissed.
3. **Is this worth the author's time?** Would a senior engineer comment on this in a real review, or would they let it go?
4. **Is this a duplicate?** If multiple lenses flagged the same concern, keep the most detailed version and note which lenses agreed.

### Concrete dismissal triggers

Dismiss a finding if ANY of these apply:

- **Style-only nit with no risk** — naming preference, formatting, comment wording that does not affect clarity or correctness.
- **Restates the PR description** — "this PR adds X" without identifying a problem with X.
- **Vague "consider" without alternative** — "consider handling errors better" with no specific suggestion.
- **Already addressed in existing review comments** — another reviewer already raised this and the author responded (only applies if prior comments are available).
- **Outside the diff** — references code that was not changed in this PR (unless it is a cross-file logic error caused by the change).
- **Hypothetical concern with no evidence** — "this could be a problem if..." where the scenario is unlikely given the context.

Classify each finding:

- **Keep** — actionable, correct, worth the author's time.
- **Drop** — low value, vague, likely false positive, or not worth mentioning.

## Merge and Deduplicate

After filtering kept findings:

1. Group by file path.
2. Within each file, order by line number (ascending).
3. Deduplicate: if multiple lenses flagged the same file + line + similar concern, merge into one finding. Note all contributing lenses in the detail.
4. Tag each finding with severity: **critical** / **suggestion** / **nit** / **praise**.

## Voice Rules

All review text — the review body AND inline comments — follows these rules:

- Lead with the technical point. No preamble.
- Short, direct sentences.
- "None of these are blocking — just flagging" prefix for nit-level groups.
- "I think we need..." for suggestions you feel strongly about.
- No "Great PR!" or "Thanks for the contribution!" openers.
- No emoji, no exclamation marks for enthusiasm.
- When uncertain, say "I might be wrong, but..." or "I'm curious about..." rather than hedging with "perhaps consider...".

## Build the Review Body

The review body is the top-level comment on the review. Keep it **one sentence** — a quick overall sentiment. The review body is not threaded (nobody can reply to it directly), so it is the wrong place for substantive feedback. All real findings go in inline comments.

Examples:

- "This looks great."
- "This looks good overall — just a few comments."
- "A couple of things to sort out before this ships."
- "One thing I'd want to address before merging."

Match the tone to the severity mix: if everything is nits/praise, keep it positive. If there are critical findings, signal that briefly.

**Findings that don't map to a diff line:** If any kept findings could not be pinned to a specific line (cross-cutting concern, or about a file as a whole), add them as a short bulleted list below the one-liner. Keep each bullet to one sentence. This is the only case where the body exceeds one line.

## Output Format

Return the final review as a structured object the harness can post:

```yaml
body: "One-sentence overall sentiment. Optional bullets for non-line findings."
comments:
  - path: "relative/path/to/file.go"
    line: 42
    severity: critical | suggestion | nit | praise
    body: "Inline comment text following the voice rules. Tag with severity prefix, e.g. '[critical] ...'."
```

**Line constraint.** Every inline `line` value MUST be a line that appears in the diff (added or context lines on the `RIGHT` side). Findings that don't satisfy this go in the review body instead of `comments`.

If the harness expects a different schema (e.g., the GitHub Reviews API payload directly), adapt the field names but preserve the structure: a single review body plus zero or more line-anchored inline comments.
