# Video Conventions

These rules MUST be followed for all demo videos.

## Pipeline

| Step | Tool | Details |
|------|------|---------|
| Slides | Python + Pillow | Generate 1920×1080 PNG images |
| Narration | `edge-tts` | `--voice en-US-GuyNeural --rate=+5%`, free neural TTS, no API key |
| Audio convert | `ffmpeg` | MP3 → WAV per slide |
| Per-slide video | `ffmpeg` | `-loop 1 -tune stillimage -c:a aac -b:a 192k -pix_fmt yuv420p` |
| Concatenate | `ffmpeg` | `-f concat -safe 0` with file list |

Pronunciation fix: `re.sub(r'\bazd\b', 'AZ-D', text, flags=re.IGNORECASE)` — applied to narration strings ONLY.

## Video Rules

| Rule | Details |
|------|---------|
| Resolution | 1920×1080 |
| Duration | Under 2-3 minutes |
| Audio padding | +0.8s silence after each slide's narration |
| PR references | NEVER mention PR numbers in videos |
| End slide | Always end with "Give it a try." + links |
| --no-prompt | Frame as built for AI agents (Copilot CLI, Claude, Gemini), NOT generic CI |
| Slide text | Lowercase `azd` always (phonetic `AZ-D` in narration only) |

## Color Palette

| Name | RGB | Usage |
|------|-----|-------|
| Background | `(15, 17, 23)` | Slide background |
| Text | `(230, 237, 243)` | Body text |
| Accent blue | `(88, 166, 255)` | Titles, labels, `→` lines |
| Green | `(63, 185, 80)` | Success, `$` prompts, `✓` lines, terminal dot |
| Red | `(248, 81, 73)` | Errors, terminal dot |
| Yellow | `(210, 153, 34)` | Warnings, section headers (`##`), terminal dot |
| Dim | `(125, 133, 144)` | Subtitles, comments, footers |
| Code BG | `(22, 27, 34)` | Code block background |
| Divider | `(48, 54, 61)` | Horizontal rule on content slides |

## Fonts

| Platform | Body | Code |
|----------|------|------|
| macOS | `/System/Library/Fonts/SFNS.ttf` | `/System/Library/Fonts/SFNSMono.ttf` |
| Windows | `C:/Windows/Fonts/arial.ttf` | `C:/Windows/Fonts/consola.ttf` |
| Fallback | `ImageFont.load_default()` | `ImageFont.load_default()` |

## Code Block Rendering

- Rounded rectangle with `radius=12`, filled with Code BG
- Terminal dots at top-left (red, yellow, green circles, 14px, spaced 22px)
- Syntax coloring by line prefix:
  - `#` or `//` → Dim (comment)
  - `$` → Green (shell prompt)
  - `error`/`fail` → Red
  - `warning` → Yellow
  - `✓`/`success` → Green
  - `>`/`→` → Accent blue
- Mono font at 18px, line height 28px

## Slide Types

### Title Slide
- "azure developer cli" label top center (accent blue, font 22)
- Large title centered (text color, font 52)
- Subtitle in dim (font 26)
- Optional bullet items with `→` prefix (font 28, 50px spacing)
- Date footer bottom center (dim, font 20)

### Content Slide
- Title top-left in accent blue (font 40)
- Horizontal divider line at y=115
- Bullet points with `•` prefix (font 24, 42px spacing)
- Lines starting with `##` render as yellow section headers (font 26)
- Optional code block positioned at bottom (min y=480)

### End Slide
- Title in accent blue centered (font 48)
- "Give it a try." below (text color, font 36)
- `aka.ms/azd  •  github.com/Azure/azure-dev` footer (dim, font 22)
