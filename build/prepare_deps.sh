#!/bin/bash
set -ex

OPUS_VERSION=$1
OPUS_SHA=$2
WHISPER_VERSION=$3
WHISPER_SHA=$4
MODELS=$5

cd /tmp && \
wget https://downloads.xiph.org/releases/opus/opus-${OPUS_VERSION}.tar.gz && \
echo "${OPUS_SHA} opus-${OPUS_VERSION}.tar.gz" | sha256sum --check && \
tar xf opus-${OPUS_VERSION}.tar.gz && \
cd opus-${OPUS_VERSION} && \
./configure && \
make && \
cd /tmp && \
wget https://github.com/ggerganov/whisper.cpp/archive/refs/tags/v${WHISPER_VERSION}.tar.gz && \
echo "${WHISPER_SHA} v${WHISPER_VERSION}.tar.gz" | sha256sum --check && \
tar xf v${WHISPER_VERSION}.tar.gz && \
cd whisper.cpp-${WHISPER_VERSION} && \
for model in ${MODELS}; do ./models/download-ggml-model.sh "${model}.en"; done && \
make libwhisper.a
