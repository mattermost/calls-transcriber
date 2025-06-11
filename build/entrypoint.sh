#!/usr/bin/env bash
set -euxo pipefail

# Create scoped (by jobID) data path.
mkdir -p /data/$TRANSCRIPTION_ID

# Give permission to write transcription files.
chown -R calls:calls /data/$TRANSCRIPTION_ID

# Run job as unprivileged user.
exec runuser -u calls "$@"
