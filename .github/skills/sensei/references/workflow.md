# Workflow

### Step 1 — Identify Target Skills

If the user specifies a skill name, target that skill. Otherwise, discover all
skills in `.github/skills/` and ask via `ask_user` which to evaluate.

### Step 2 — Check Compliance

For each target skill, run:

```bash
waza check .github/skills/{skill-name}
```

Parse: Compliance Score, Spec Compliance (X/8), Token Budget, Evaluation Suite status.

### Step 3 — Run Eval Suite (if exists)

If `.github/skills/{skill-name}/evals/eval.yaml` exists:

```bash
waza run .github/skills/{skill-name}/evals/eval.yaml -v
```

Report tasks passed/total. If no eval suite, offer to scaffold via `ask_user`:

```bash
waza new {skill-name} --output-dir .github/skills/{skill-name}
```

### Step 4 — Improve (Iterative)

Fix each compliance issue found in Step 2:
- **Missing triggers/anti-triggers**: Add USE FOR / DO NOT USE FOR sections
- **Token budget exceeded**: Move content to `references/` using `{{ references/file.md }}`
- **Missing license/version**: Add to frontmatter
- **Missing eval suite**: Scaffold with waza

Re-run `waza check` after each fix.

### Step 5 — Re-check and Report

Run final `waza check` and `waza run` (if evals exist). Present before→after comparison
via `ask_user` with choices: Accept changes, Continue improving, Discard changes.

### Step 6 — Iterate

If "Continue improving", return to Step 4. Maximum 5 iterations per skill.

## Batch Mode

For multiple skills: process sequentially through Steps 2-5, per-skill summary after each,
batch summary at the end.

## Error Handling

- **waza not installed** → offer: `go install github.com/microsoft/waza/cmd/waza@latest`
- **Skill not found** → list available skills, ask user to select
- **Eval run fails** → report error, continue with compliance check only
- **waza suggest fails** → fall back to manual improvement suggestions
