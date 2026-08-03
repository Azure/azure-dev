#!/usr/bin/env bash
#
# Fetches the official `microsoft-foundry` skill from microsoft/azure-skills into
# skills/.external/ so vally evals can load it via `environment.skills`. We fetch
# rather than vendor: it is a few hundred files owned by another team.
#
# Usage:
#   ./scripts/fetch-foundry-skill.sh            # track main (default)
#   ./scripts/fetch-foundry-skill.sh <ref>      # pin to a branch, tag, or commit SHA
#   AZURE_SKILLS_REF=<ref> ./scripts/fetch-foundry-skill.sh
#
# Since `main` moves, the resolved commit SHA is written to skills/.external/.skill-ref
# so a run can be traced back to the skill content behind it.
#
set -euo pipefail

REPO_URL="${AZURE_SKILLS_REPO:-https://github.com/microsoft/azure-skills.git}"
REF="${1:-${AZURE_SKILLS_REF:-main}}"
SKILL_PATH="skills/microsoft-foundry"

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
evals_dir="$(dirname "$script_dir")"
dest_root="$evals_dir/skills/.external"
dest="$dest_root/microsoft-foundry"
stamp="$dest_root/.skill-ref"

# Clone into a temp dir and swap it in, so an interrupted fetch can't leave a
# half-populated skill directory that evals would silently load.
tmp_dir="$(mktemp -d)"
cleanup() { rm -rf "$tmp_dir"; }
trap cleanup EXIT

echo "Fetching $SKILL_PATH from $REPO_URL@$REF ..." >&2

# --branch only accepts branches and tags, so fall back to clone+checkout when
# REF is a commit SHA.
if ! git clone --quiet --depth 1 --filter=blob:none --sparse \
    --branch "$REF" "$REPO_URL" "$tmp_dir/azure-skills" 2>/dev/null; then
    rm -rf "$tmp_dir/azure-skills"
    git clone --quiet --filter=blob:none --sparse "$REPO_URL" "$tmp_dir/azure-skills"
    git -C "$tmp_dir/azure-skills" checkout --quiet "$REF"
fi

git -C "$tmp_dir/azure-skills" sparse-checkout set --no-cone "$SKILL_PATH"

src="$tmp_dir/azure-skills/$SKILL_PATH"
if [[ ! -f "$src/SKILL.md" ]]; then
    echo "error: $SKILL_PATH/SKILL.md not found at $REF -- has the skill moved?" >&2
    exit 1
fi

resolved_sha="$(git -C "$tmp_dir/azure-skills" rev-parse HEAD)"

mkdir -p "$dest_root"
rm -rf "$dest"
mv "$src" "$dest"

cat >"$stamp" <<EOF
repo=$REPO_URL
ref=$REF
commit=$resolved_sha
fetched_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)
EOF

echo "Fetched microsoft-foundry skill @ ${resolved_sha:0:12} -> $dest" >&2
