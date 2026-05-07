---
name: weekly-demo-video
license: MIT
metadata:
  version: "1.0"
description: >-
  Generate narrated weekly demo videos for azd features. Pulls latest commits,
  identifies demo-worthy features, researches PRs, and produces MP4 videos with
  dark-themed slides and neural TTS narration.

  INVOKES: Python, Pillow, ffmpeg, edge-tts, explore sub-agents, ask_user.

  USE FOR: weekly demo, generate demo video, demo video, sprint demo, create demo,
  make demo video, demo for LT, weekly demo video, azd demo.
  DO NOT USE FOR: general video editing, non-azd demos, slide decks without video,
  weekly reports (use weekly-report).
---

# Weekly Demo Video Generator

Generates narrated MP4 demo videos for azd features using Python + Pillow + edge-tts + ffmpeg.

## Prerequisites

Ensure these tools are installed:

| Tool | Purpose |
|------|---------|
| Python 3 | Script execution |
| Pillow | Slide generation (`pip install Pillow`) |
| ffmpeg | Video/audio stitch |
| edge-tts | Neural TTS (`pip install edge-tts`) |

Check: `python3 -c "from PIL import Image; print('ok')" && ffmpeg -version >/dev/null 2>&1 && edge-tts --list-voices >/dev/null 2>&1`

## Execution Flow

### Step 1: Pull latest

```bash
cd <repo-root>  # the azure-dev repository root
git checkout main && git pull --rebase
```

### Step 2: Find commits for the week

```bash
# Example: Thursday May 1 to Thursday May 8
git log --oneline --since="2026-05-01" --until="2026-05-08" --no-merges
```

Use the current week window (7 days). For sprint demos, use a 2-week window.

### Step 3: Identify demo-worthy features

Group related commits. Skip: typos, CI fixes, test-only, deps bumps, docs-only.
Look for: new commands, UX improvements, perf gains, new flags, agent features.

Use explore agents in parallel to research each feature group (give them specific commit SHAs).

### Step 4: Confirm with user

Present a table of proposed demos. Ask user to confirm or adjust before generating.

### Step 5: Generate videos

Follow the conventions strictly:

{{ references/CONVENTIONS.md }}

### Step 6: Report

List generated videos with filenames and durations. Offer short descriptions for docs.

## Error Handling

- **edge-tts failure**: Retry once. If it fails again, log the error and skip that slide's audio — notify the user.
- **ffmpeg failure**: Check the ffmpeg error output. Common issues: missing codec, invalid image path. Print the error and stop — don't produce a partial video.
- **Font not found**: Falls back to `ImageFont.load_default()` automatically. Warn the user that slides may look different.

## Output

All videos go to: `<repo-root>/demo-video/`

## Demo Naming

- **Weekly demos**: `azd_weekly_demo_{date}_{name}.mp4` (e.g. `azd_weekly_demo_may_07_exegraph.mp4`)
- **Sprint demos**: `azd_sprint_demo_{date}_{name}.mp4` (e.g. `azd_sprint_demo_apr_28_agent_sessions.mp4`)

