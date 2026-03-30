#!/usr/bin/env bash
set -euxo pipefail

# If a custom CA cert is mounted, add it to the system trust store so that
# all TLS connections trust the certificate.
if [ -n "${TLS_CA_CERT_FILE:-}" ] && [ -f "${TLS_CA_CERT_FILE}" ]; then
    cp "${TLS_CA_CERT_FILE}" /usr/local/share/ca-certificates/custom-ca.crt
    update-ca-certificates
fi

# Create scoped (by jobID) data path.
mkdir -p /data/$TRANSCRIPTION_ID

# Give permission to write transcription files.
chown -R calls:calls /data/$TRANSCRIPTION_ID

# Run job as unprivileged user.
exec runuser -u calls "$@"
