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
if [ "$TARGET_ARCH" == "arm64" ]; then
	ONNX_ARCH=aarch64
	ONNX_SHA=c1dcd8ab29e8d227d886b6ee415c08aea893956acf98f0758a42a84f27c02851
fi

UNAME_M=$(uname -m)
if [ "$IS_M1" == "true" ]; then
	echo "Overriding UNAME_M on detected M1 host";
	UNAME_M="arm64"
fi

cd /tmp && \
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
([[ "$TARGET_ARCH" == "amd64" ]] && [[ "$IS_BUILD" == "true" ]] && echo "Patching Whisper.CPP Makefile" && patch -p1 Makefile /src/build/whisper.patch) || true && \
for model in ${MODELS}; do ./models/download-ggml-model.sh "${model}"; done && \
make -j4 libwhisper.a UNAME_M=${UNAME_M} && \
cd /tmp && \
wget https://github.com/microsoft/onnxruntime/releases/download/v${ONNX_VERSION}/onnxruntime-linux-${ONNX_ARCH}-${ONNX_VERSION}.tgz && \
echo "${ONNX_SHA} onnxruntime-linux-${ONNX_ARCH}-${ONNX_VERSION}.tgz" | sha256sum --check && \
tar xf onnxruntime-linux-${ONNX_ARCH}-${ONNX_VERSION}.tgz && \
mv onnxruntime-linux-${ONNX_ARCH}-${ONNX_VERSION} onnxruntime-linux-${ONNX_VERSION} && \
wget https://csspeechstorage.blob.core.windows.net/drop/${AZURE_SDK_VERSION}/SpeechSDK-Linux-${AZURE_SDK_VERSION}.tar.gz && \
echo "${AZURE_SDK_SHA} SpeechSDK-Linux-${AZURE_SDK_VERSION}.tar.gz" | sha256sum --check && \
tar xf SpeechSDK-Linux-${AZURE_SDK_VERSION}.tar.gz && \
mv /tmp/SpeechSDK-Linux-${AZURE_SDK_VERSION}/lib/x64 /tmp/SpeechSDK-Linux-${AZURE_SDK_VERSION}/lib/amd64
