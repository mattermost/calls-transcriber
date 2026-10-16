#!/usr/bin/env bash
# Build the transcriber docker image from the current checkout (including
# uncommitted changes) and tag it for a local DEV_MODE calls-offloader, which
# always runs calls-transcriber:master.
#
# Works from a git worktree. The image build needs a real .git directory
# (the Makefile runs git inside the container), but a worktree's .git is a
# file pointing at a host path. So we build from a throwaway clone with the
# working tree copied over it.
#
# Usage: build/local-image.sh [docker-tag]   (default: calls-transcriber:master)
#        make docker-build-local
set -euo pipefail

TAG="${1:-calls-transcriber:master}"
SRC="$(git rev-parse --show-toplevel)"
TMP="$(mktemp -d "${TMPDIR:-/tmp}/calls-transcriber-build.XXXXXX")"
trap 'rm -rf "$TMP"' EXIT

git clone -q --no-hardlinks "$SRC" "$TMP"
git -C "$TMP" remote set-url origin "$(git -C "$SRC" config --get remote.origin.url)"
rsync -a --delete --exclude .git --exclude dist "$SRC"/ "$TMP"/

# CI=false so an inherited CI=true can't switch the Makefile to a multi-arch
# build that pushes to a registry instead of loading the image locally.
make -C "$TMP" docker-build CI=false DOCKER_TAG="$TAG"
docker image ls "$TAG"
