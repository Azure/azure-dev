---
name: weekly-demo-video
description: >-
  Generate narrated weekly demo videos for azd features. Pulls latest commits,
  identifies demo-worthy features, researches PRs, and produces MP4 videos with
  dark-themed slides and neural TTS narration.
  USE FOR: weekly demo, generate demo video, demo video, sprint demo, create demo,
  make demo video, demo for LT, weekly demo video, azd demo.
  DO NOT USE FOR: general video editing, non-azd demos, slide decks without video.
---

# Weekly Demo Video Generator

Generates narrated MP4 demo videos for azd features using Python + Pillow + edge-tts + ffmpeg.

## Prerequisites

| Tool | Purpose | Install |
|------|---------|---------|
| Python 3 | Script execution | `brew install python` |
| Pillow | Slide generation | `pip3 install Pillow` |
| ffmpeg | Video/audio stitch | `brew install ffmpeg` |
| edge-tts | Neural TTS | `pip3 install edge-tts` |

Check: `python3 -c "from PIL import Image; print('ok')" && ffmpeg -version >/dev/null 2>&1 && edge-tts --list-voices >/dev/null 2>&1`

## Execution Flow

### Step 1: Pull latest

```bash
cd <repo-root>  # the azure-dev repository root
git checkout main && git pull --rebase
```

### Step 2: Find commits for the week

```bash
git log --oneline --since="YYYY-MM-DD" --until="YYYY-MM-DD" --no-merges
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

## Output

All videos go to: `<repo-root>/demo-video/`

## Demo Naming

- **Weekly demos**: `azd_weekly_demo_{date}_{name}.mp4` (e.g. `azd_weekly_demo_may_07_exegraph.mp4`)
- **Sprint demos**: `azd_sprint_demo_{date}_{name}.mp4` (e.g. `azd_sprint_demo_apr_28_agent_sessions.mp4`)

