#!/bin/bash
set -ex

OPUS_VERSION=$1
WHISPER_VERSION=$2
MODELS=$3

cd /tmp && \
wget https://downloads.xiph.org/releases/opus/opus-${OPUS_VERSION}.tar.gz && \
tar xf opus-${OPUS_VERSION}.tar.gz && \
cd opus-${OPUS_VERSION} && \
./configure && \
make && \
cd /tmp && \
wget https://github.com/ggerganov/whisper.cpp/archive/refs/tags/v${WHISPER_VERSION}.tar.gz && \
tar xf v${WHISPER_VERSION}.tar.gz && \
cd whisper.cpp-${WHISPER_VERSION} && \
for model in ${MODELS}; do ./models/download-ggml-model.sh "${model}.en"; done && \
make libwhisper.a
