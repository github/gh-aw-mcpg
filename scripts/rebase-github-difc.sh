#!/bin/bash
# Script to rebase lpcox/github-difc onto origin/main

set -e

echo "=== Rebasing lpcox/github-difc onto origin/main ==="

# Fetch latest from origin
echo "Fetching latest from origin..."
git fetch origin

# Check current branch
CURRENT_BRANCH=$(git branch --show-current)
echo "Current branch: $CURRENT_BRANCH"

# Stash any uncommitted changes
if ! git diff --quiet || ! git diff --cached --quiet; then
    echo "Stashing uncommitted changes..."
    git stash push -m "Auto-stash before rebase"
    STASHED=1
else
    STASHED=0
fi

# Checkout the branch to rebase
echo "Checking out lpcox/github-difc..."
git checkout lpcox/github-difc

# Perform the rebase
echo "Rebasing onto origin/main..."
if git rebase origin/main; then
    echo "✓ Rebase completed successfully!"
else
    echo "✗ Rebase failed with conflicts."
    echo ""
    echo "To resolve:"
    echo "  1. Fix conflicts in the listed files"
    echo "  2. git add <fixed-files>"
    echo "  3. git rebase --continue"
    echo ""
    echo "To abort: git rebase --abort"
    exit 1
fi

# Return to original branch if different
if [ "$CURRENT_BRANCH" != "lpcox/github-difc" ]; then
    echo "Returning to original branch: $CURRENT_BRANCH"
    git checkout "$CURRENT_BRANCH"
fi

# Restore stashed changes
if [ "$STASHED" -eq 1 ]; then
    echo "Restoring stashed changes..."
    git stash pop
fi

echo ""
echo "=== Done ==="
echo "To push the rebased branch: git push --force-with-lease origin lpcox/github-difc"
