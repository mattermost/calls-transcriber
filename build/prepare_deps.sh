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
IS_BUILD=${10}
ONNX_ARCH=x64
ONNX_SHA=a0994512ec1e1debc00c18bfc7a5f16249f6ebd6a6128ff2034464cc380ea211
CMAKE_VERSION="4.0.3"
CMAKE_ARCH=x86_64
CMAKE_SHA=585ae9e013107bc8e7c7c9ce872cbdcbdff569e675b07ef57aacfb88c886faac
if [ "$TARGET_ARCH" == "arm64" ]; then
	ONNX_ARCH=aarch64
	CMAKE_ARCH=aarch64
	ONNX_SHA=c1dcd8ab29e8d227d886b6ee415c08aea893956acf98f0758a42a84f27c02851
	CMAKE_SHA=391da1544ef50ac31300841caaf11db4de3976cdc4468643272e44b3f4644713
fi

CMAKE_ARGS=""

if [ "$IS_M1" == "true" ]; then
	echo "Adding CMAKE_ARGS on detected M1 host";
	CMAKE_ARGS="-DGGML_NATIVE=OFF -DGGML_CPU_ARM_ARCH=armv8-a"
fi

CMAKE_BASE="cmake-$CMAKE_VERSION-linux-$CMAKE_ARCH"
CMAKE_PATH="/tmp/$CMAKE_BASE/bin"

cd /tmp && \
wget "https://github.com/Kitware/CMake/releases/download/v$CMAKE_VERSION/$CMAKE_BASE.tar.gz" && \
echo "$CMAKE_SHA $CMAKE_BASE.tar.gz" | sha256sum --check && \
tar xf "$CMAKE_BASE.tar.gz" && \
wget https://downloads.xiph.org/releases/opus/opus-${OPUS_VERSION}.tar.gz && \
echo "${OPUS_SHA} opus-${OPUS_VERSION}.tar.gz" | sha256sum --check && \
tar xf opus-${OPUS_VERSION}.tar.gz && \
cd opus-${OPUS_VERSION} && \
./configure && \
make -j4 && \
cd /tmp && \
wget https://github.com/ggerganov/whisper.cpp/archive/refs/tags/v${WHISPER_VERSION}.tar.gz && \
echo "${WHISPER_SHA} v${WHISPER_VERSION}.tar.gz" | sha256sum --check && \
tar xf v${WHISPER_VERSION}.tar.gz && \
cd whisper.cpp-${WHISPER_VERSION} && \
for model in ${MODELS}; do ./models/download-ggml-model.sh "${model}"; done && \
PATH="$PATH:$CMAKE_PATH" cmake -B build ${CMAKE_ARGS} && \
PATH="$PATH:$CMAKE_PATH" cmake --build build -j --config Release && \
cd /tmp && \
wget https://github.com/microsoft/onnxruntime/releases/download/v${ONNX_VERSION}/onnxruntime-linux-${ONNX_ARCH}-${ONNX_VERSION}.tgz && \
echo "${ONNX_SHA} onnxruntime-linux-${ONNX_ARCH}-${ONNX_VERSION}.tgz" | sha256sum --check && \
tar xf onnxruntime-linux-${ONNX_ARCH}-${ONNX_VERSION}.tgz && \
mv onnxruntime-linux-${ONNX_ARCH}-${ONNX_VERSION} onnxruntime-linux-${ONNX_VERSION} && \
wget https://csspeechstorage.blob.core.windows.net/drop/${AZURE_SDK_VERSION}/SpeechSDK-Linux-${AZURE_SDK_VERSION}.tar.gz && \
echo "${AZURE_SDK_SHA} SpeechSDK-Linux-${AZURE_SDK_VERSION}.tar.gz" | sha256sum --check && \
tar xf SpeechSDK-Linux-${AZURE_SDK_VERSION}.tar.gz && \
mv /tmp/SpeechSDK-Linux-${AZURE_SDK_VERSION}/lib/x64 /tmp/SpeechSDK-Linux-${AZURE_SDK_VERSION}/lib/amd64
