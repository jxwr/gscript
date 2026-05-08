#!/usr/bin/env bash
set -euo pipefail

remote="${1:-origin}"
branch="${2:-main}"
repo_url="${GIT_HTTPS_REMOTE_URL:-https://github.com/jxwr/gscript.git}"

git push "$repo_url" "HEAD:${branch}"
