#!/bin/sh
set -ex

OPUS_VERSION=$1
OPUS_SHA=$2
WHISPER_VERSION=$3
WHISPER_SHA=$4

OPUS_INCLUDE_PATH="/tmp/opus-${OPUS_VERSION}/include"
WHISPER_INCLUDE_PATH="/tmp/whisper.cpp-${WHISPER_VERSION}"
OPUS_LIBRARY_PATH="/tmp/opus-${OPUS_VERSION}/.libs"
WHISPER_LIBRARY_PATH=${WHISPER_INCLUDE_PATH}

bash ./build/prepare_deps.sh ${OPUS_VERSION} ${OPUS_SHA} ${WHISPER_VERSION} ${WHISPER_SHA} && \
C_INCLUDE_PATH="${OPUS_INCLUDE_PATH}:${WHISPER_INCLUDE_PATH}" \
LIBRARY_PATH="${OPUS_LIBRARY_PATH}:${WHISPER_LIBRARY_PATH}" \
golangci-lint run ./...
