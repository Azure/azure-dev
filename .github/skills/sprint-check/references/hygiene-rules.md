# Issue Hygiene Rules

Shared validation rules for sprint-check and issue-cleanup skills.

## Required Fields by Milestone Tier

| Milestone tier | Must have |
|---|---|
| Future, Backlog, Backlog Candidates | labels (at least one `area/*`), milestone |
| On Deck, current/upcoming month | labels, milestone, Priority (project), Initiative (project), EPIC parent (if quarterly initiative) |
| Current Sprint | all above + assigned to someone |

## Field Checks

### 1. Labels
- **Rule**: every issue must have at least one `area/*` label
- **Check**: `labels.nodes` contains at least one label starting with `area/`
- **Fix**: present the area label list, let user pick
- **Note**: type labels (`bug`, `enhancement`, `feature`) are nice-to-have, not required

### 2. Milestone
- **Rule**: every issue in the current sprint should have the current month's milestone
- **Current month milestone**: derive from today's date → "May 2026", "June 2026", etc.
- **Check**: `milestone.title` matches current month
- **Allowed exceptions**: On Deck, Backlog (for items being tracked but not committed this month)
- **Fix**: `gh issue edit NUMBER --milestone "May 2026"`

### 3. Priority (Project Field)
- **Rule**: issues in On Deck or current sprint must have Priority set
- **Check**: `fieldValueByName(name: "Priority")` is not null
- **Fix**: present Priority options, let user pick, then mutate via GraphQL

### 4. Initiative (Project Field)
- **Rule**: issues in On Deck or current sprint must have Initiative set
- **Check**: `fieldValueByName(name: "Initiative")` is not null
- **Fix**: present Initiative options, let user pick, then mutate via GraphQL
- **Special**: 🛡️ Ongoing initiative allows direct issues (no EPIC parent needed)

### 5. EPIC Parent
- **Rule**: if initiative is a quarterly initiative (not 🛡️ Ongoing), the issue should have an EPIC parent
- **Check**: `parent` field is not null (GitHub sub-issues)
- **Note**: this is a warning, not a blocker — some standalone items under quarterly initiatives are valid
- **Fix**: cannot auto-fix, suggest to user which EPICs exist under that initiative

### 6. Assignment
- **Rule**: issues in the current sprint must be assigned to someone
- **Check**: `assignees.nodes` is not empty
- **Fix**: `gh issue edit NUMBER --add-assignee "@me"` or ask who to assign

## Customer-Reported Special Rules

Customer-reported issues (`customer-reported` label) get elevated priority:
- **No milestone** → 🔴 Critical (should be triaged into current milestone)
- **No area label** → 🔴 Critical (can't route to the right team)
- **Has `needs-triage`** → expected, not a problem (pending triage)
- **Has `needs-team-attention`** → expected after milestone is set

## Classification Order (for suggesting placement)

When an issue has no milestone or initiative:
1. **Planned priorities** — fits a current quarter initiative/EPIC? → parent it there
2. **Customer-reported / regression** → current milestone + 🛡️ Ongoing
3. **One-off items** (engsys, pipeline, test) → current milestone + 🛡️ Ongoing
4. **None of the above** → Backlog, Backlog Candidates, or Future
