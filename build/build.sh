#!/bin/bash
set -ex

OPUS_VERSION=$1
OPUS_SHA=$2
WHISPER_VERSION=$3
WHISPER_SHA=$4
MODELS=$5
ONNX_VERSION=$6
TARGET_ARCH=$7
AZURE_SDK_VERSION=$8
AZURE_SDK_SHA=$9

OPUS_INCLUDE_PATH="/tmp/opus-${OPUS_VERSION}/include"
WHISPER_INCLUDE_PATH="/tmp/whisper.cpp-${WHISPER_VERSION}/include:/tmp/whisper.cpp-${WHISPER_VERSION}/ggml/include"
OPUS_LIBRARY_PATH="/tmp/opus-${OPUS_VERSION}/.libs"
WHISPER_LIBRARY_PATH="/tmp/whisper.cpp-${WHISPER_VERSION}"
ONNX_INCLUDE_PATH="/tmp/onnxruntime-linux-${ONNX_VERSION}/include"
ONNX_LIBRARY_PATH="/tmp/onnxruntime-linux-${ONNX_VERSION}/lib"
AZURE_SDK_INCLUDE_PATH="/tmp/SpeechSDK-Linux-${AZURE_SDK_VERSION}/include/c_api"
AZURE_SDK_LIBRARY_PATH="/tmp/SpeechSDK-Linux-${AZURE_SDK_VERSION}/lib/${TARGET_ARCH}"

# Only fetch dependencies if needed (e.g. not already cached by Docker).
if [ ! -d "$OPUS_INCLUDE_PATH" ]; then
	echo "Missing dependencies, downloading."
	bash ./build/prepare_deps.sh ${OPUS_VERSION} ${OPUS_SHA} ${WHISPER_VERSION} ${WHISPER_SHA} "${MODELS}" ${ONNX_VERSION} ${TARGET_ARCH} ${AZURE_SDK_VERSION} ${AZURE_SDK_SHA}
fi

C_INCLUDE_PATH="${OPUS_INCLUDE_PATH}:${WHISPER_INCLUDE_PATH}:${ONNX_INCLUDE_PATH}:${AZURE_SDK_INCLUDE_PATH}" \
LIBRARY_PATH="${OPUS_LIBRARY_PATH}:${WHISPER_LIBRARY_PATH}:${ONNX_LIBRARY_PATH}:${AZURE_SDK_LIBRARY_PATH}" \
LD_RUN_PATH="${ONNX_LIBRARY_PATH}:${AZURE_SDK_LIBRARY_PATH}" \
CGO_LDFLAGS="-lMicrosoft.CognitiveServices.Speech.core" \
make go-build
