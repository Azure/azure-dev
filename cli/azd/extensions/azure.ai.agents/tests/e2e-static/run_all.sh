#!/bin/bash
# Run all E2E test tiers.
# Usage: bash run_all.sh [--skip-tier2]
#
# Prerequisites:
#   - Run from WSL with azd, az CLI, tmux available
#   - Token cache pre-warmed (prewarm_tokens.py)
#   - GitHub token available via gh.exe or $GITHUB_TOKEN

set -e
export HOME=${HOME:-/home/wsladmin}
export PATH=/usr/local/bin:$HOME/bin:$HOME/.pyenv/versions/3.12.3/bin:/usr/bin:/bin

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
cd "$SCRIPT_DIR"

SKIP_TIER2=false
for arg in "$@"; do
    case $arg in
        --skip-tier2) SKIP_TIER2=true ;;
    esac
done

echo "============================================================"
echo "E2E TEST SUITE — $(date)"
echo "============================================================"

# Tier 0: Offline validation (~15s)
echo ""
echo ">>> TIER 0: Offline Validation"
python3 test_tier0.py
echo ""

# Tier 1: Init variants (~3min)
echo ">>> TIER 1: Init Variants"
python3 test_tier1.py
echo ""

# Tier 2: Full golden path (~15min parallel)
if [ "$SKIP_TIER2" = false ]; then
    echo ">>> TIER 2: Golden Path (code + container, parallel)"
    python3 test_tier2.py
else
    echo ">>> TIER 2: SKIPPED (--skip-tier2)"
fi

echo ""
echo "============================================================"
echo "ALL TIERS COMPLETE"
echo "============================================================"
