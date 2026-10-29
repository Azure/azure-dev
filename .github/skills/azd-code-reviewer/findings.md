# Findings

Self-reflection triage, voice rules, and output format for the review.

## Self-reflection pass

Before emitting findings, run a quality filter. The goal is high signal-to-noise — the author should want to act on most findings.

For each finding, evaluate:

1. **Is this useful?** Criticism must suggest a specific change or raise a specific concern. Praise must identify a concrete pattern worth preserving.
2. **Is this correct?** Does the finding accurately reflect what the code does? Cross-check against the actual diff — findings based on misreading the code should be dismissed.
3. **Is this worth the author's time?** Would a senior engineer comment on this in a real review, or would they let it go?
4. **Is this a duplicate?** If multiple lenses flagged the same concern, keep the most detailed version and note which lenses agreed.

### Concrete dismissal triggers

Dismiss a finding if ANY of these apply:

- **Style-only nit with no risk** — naming preference, formatting, comment wording that does not affect clarity or correctness.
- **Restates the PR description** — "this PR adds X" without identifying a problem with X.
- **Vague "consider" without alternative** — "consider handling errors better" with no specific suggestion.
- **Already addressed in existing review comments** — another reviewer already raised this and the author responded (only applies if prior comments are available).
- **Outside the diff** — references code unrelated to the PR. Missing companion changes required by the PR, including absent scaffold files, remain in scope.
- **Hypothetical concern with no evidence** — "this could be a problem if..." where the scenario is unlikely given the context.

**Extension contract exception.** Findings from `extension-user-contract-review` request the approved experiences spec
for an evidenced contract change. Do not dismiss them as restating the PR description merely because the change is
intentional or correctly implemented. Apply that skill's thread-specific duplicate and spec-link rules; an unanswered
existing thread is not a reason to post another comment. Missing review history is an evidence limitation, not proof
that a spec is missing.

Contract findings are mandatory policy checks, not optional suggestions for the ordinary 0-3 per-lens target.
Reserve output capacity for them before optional findings. If the host's limit still prevents returning all retained
contract comments, report the omitted count and mark the pass blocked; never silently truncate.

Classify each finding:

- **Keep** — useful, correct, worth the author's time.
- **Drop** — low value, vague, likely false positive, or not worth mentioning.

## Merge and deduplicate

After filtering kept findings:

1. Group by file path.
2. Within each file, group by diff side, then order by that side's line number (ascending).
3. Deduplicate: if multiple lenses flagged the same file + side + line + similar concern, merge into one finding. Note all contributing lenses in the detail.
4. Tag each finding with severity: **critical** / **suggestion** / **nit** / **praise**.

When a correctness finding shares a root cause with an extension contract finding, keep one inline comment with the
correctness explanation, higher severity, contract marker, and experiences spec guidance. Do not merge independent
contract changes into one thread.

## Voice rules

All review text — the review body AND inline comments — follows these rules:

- Lead with the technical point. No preamble.
- Short, direct sentences.
- No "Great PR!" or "Thanks for the contribution!" openers.
- No emoji, no exclamation marks for enthusiasm.
- When uncertain, say "I might be wrong, but..." or "I'm curious about..." rather than hedging with "perhaps consider...".
- Append `<!-- azd-code-reviewer -->` to every inline comment and non-line finding bullet. Do not add the marker to the one-sentence review sentiment.

For extension contract findings, also preserve `<!-- extension-user-contract-changes-reviewer -->`, the
**User contract change** and **Experiences spec** sections, and the companion skill's spec status. Do not replace
these with a generic suggestion or drop a previously supplied spec link.

## Build the review body

The review body is the top-level comment on the review. Keep it **one sentence** — a quick overall sentiment. The review body is not threaded (nobody can reply to it directly), so it is the wrong place for substantive feedback. All real findings go in inline comments.

Examples:

- "This looks great."
- "This looks good overall — just a few comments."
- "A couple of things to sort out before this ships."
- "One thing I'd want to address before merging."

Match the tone to the severity mix: if everything is nits/praise, keep it positive. If there are critical findings, signal that briefly.

**Findings that do not map to a diff line.** If a kept finding cannot be pinned to a specific line, add it as a short bullet below the one-line review body. Keep each bullet to one sentence. This is the only case where the body exceeds one line.

**Contract completion status.** For extension PRs, append a factual status sentence to the review body after the final
coverage and outgoing-comment audit, even when there are no new findings:
`Extension user-contract review: complete (<N> files inspected; <M> new contract comments; <K> existing threads).`
Use `blocked` with the specific limitation and omitted count, if any, when the audit cannot pass. Do not claim that
comments were posted when only returning them to the host. This status is an exception to the one-sentence body
rule. If the host lacks a review body, expose it in the supported result channel, not a fabricated inline finding.

## Output format

Return the final review as a structured object the harness can post:

```yaml
body: "One-sentence overall sentiment. Optional bullets for non-line findings."
comments:
  - path: "relative/path/to/file.go"
    line: 42
    side: RIGHT
    severity: critical | suggestion | nit | praise
    body: "[critical] Inline comment text following the voice rules. <!-- azd-code-reviewer -->"
```

**Line constraint.** Every inline `line` value MUST appear on its selected diff side. Use `side: RIGHT` with new-file
coordinates for additions and modifications (or context lines for ordinary findings), and `side: LEFT` with old-file
coordinates for deletion-only findings. Existing findings without `side` default to `RIGHT`. Preserve an explicit
side and its coordinates when adapting the host schema. Ordinary findings with no diff anchor go in the review body.

**Extension contract constraint.** Contract findings require an explicit `side` and a diff line that directly
evidences the change: added or modified lines on `RIGHT`, or deleted lines on `LEFT` for deletion-only changes,
including deleted files. Unchanged context lines are not valid contract anchors. Never move these findings into
the review body or onto unrelated RIGHT-side lines; if the host cannot publish LEFT-side comments or no valid
anchor exists, retain the affected finding in the supported result channel and mark the pass blocked.

If the harness expects a different schema (e.g., the GitHub Reviews API payload directly), adapt the field names but preserve the structure: a single review body plus zero or more line-anchored inline comments.
