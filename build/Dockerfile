# This dockerfile is used to build Mattermost calls-transcriber
# A multi stage build, with golang used as a builder
# and debian:sid-slim as runner
ARG GO_VERSION
FROM --platform=$TARGETPLATFORM debian:bookworm-20240722-slim as base

# Setup system dependencies
WORKDIR /workdir

# Install TLS certificates
# hadolint ignore=DL3008
RUN apt-get update && \
    apt-get install --no-install-recommends -y ca-certificates && \
    apt-get clean && \
    rm -rf /var/lib/apt/lists/*

# Create unprivileged user to run the recorder process
RUN groupadd -r calls && useradd -mr -g calls calls

ARG GO_VERSION
FROM --platform=$TARGETPLATFORM golang:${GO_VERSION}-bookworm as builder
ARG TARGETOS
ARG TARGETARCH
ARG GO_BUILD_PLATFORMS=${TARGETOS}-${TARGETARCH}
ARG OPUS_VERSION
ARG OPUS_SHA
ARG WHISPER_VERSION
ARG WHISPER_SHA
ARG WHISPER_MODELS
ARG ONNX_VERSION
ARG DEBIAN_FRONTEND=noninteractive
ARG IS_M1
ARG AZURE_SDK_VERSION
ARG AZURE_SDK_SHA

# Setup directories structure and compile
# We first copy just the build directory so that we can properly cache dependencies which can take a while to download.
COPY ./build /src/build
WORKDIR /src
RUN /bin/bash ./build/prepare_deps.sh ${OPUS_VERSION} ${OPUS_SHA} ${WHISPER_VERSION} ${WHISPER_SHA} "${WHISPER_MODELS}" ${ONNX_VERSION} ${TARGETARCH} ${AZURE_SDK_VERSION} ${AZURE_SDK_SHA}

COPY . /src
RUN /bin/bash ./build/build.sh ${OPUS_VERSION} ${OPUS_SHA} ${WHISPER_VERSION} ${WHISPER_SHA} "${WHISPER_MODELS}" ${ONNX_VERSION} ${TARGETARCH} ${AZURE_SDK_VERSION} ${AZURE_SDK_SHA}

FROM base AS runner
ARG TARGETOS
ARG TARGETARCH
ARG WHISPER_VERSION
ARG ONNX_VERSION
ARG AZURE_SDK_VERSION
COPY --from=builder /src/dist/calls-transcriber-${TARGETOS}-${TARGETARCH} /opt/calls-transcriber/bin/calls-transcriber
COPY --from=builder /tmp/whisper.cpp-${WHISPER_VERSION}/models /models
COPY --from=builder /tmp/onnxruntime-linux-${ONNX_VERSION}/lib/* /usr/lib/
COPY --from=builder /tmp/SpeechSDK-Linux-${AZURE_SDK_VERSION}/lib/${TARGETARCH}/* /usr/lib/
COPY --from=builder /src/build/models/silero_vad.onnx /models
COPY --from=builder /src/build/entrypoint.sh .
RUN ldconfig

ENTRYPOINT ["./entrypoint.sh"]
CMD ["/opt/calls-transcriber/bin/calls-transcriber"]
