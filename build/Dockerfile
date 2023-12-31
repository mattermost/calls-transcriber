# This dockerfile is used to build Mattermost calls-transcriber
# A multi stage build, with golang used as a builder
# and debian:bookworm-slim as runner
ARG ARCH
ARG GO_IMAGE
ARG RUNNER_IMAGE

FROM ${RUNNER_IMAGE:?} as base

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

FROM ${GO_IMAGE:?} as builder
ARG ARCH
ARG OPUS_VERSION
ARG OPUS_SHA
ARG WHISPER_VERSION
ARG WHISPER_SHA
ARG WHISPER_MODELS
ARG ONNX_VERSION
ARG ONNX_SHA
ARG DEBIAN_FRONTEND=noninteractive
#GO_BUILD_PLATFORMS holds the platforms that we will build the docker image against
ARG GO_BUILD_PLATFORMS=linux-${ARCH:?}

# Setup directories structure and compile
# We first copy just the build directory so that we can properly cache dependencies which can take a while to download.
COPY ./build /src/build
WORKDIR /src
RUN /bin/bash ./build/prepare_deps.sh ${OPUS_VERSION} ${OPUS_SHA} ${WHISPER_VERSION} ${WHISPER_SHA} "${WHISPER_MODELS}" ${ONNX_VERSION} ${ONNX_SHA}

COPY . /src
RUN /bin/bash ./build/build.sh ${OPUS_VERSION} ${OPUS_SHA} ${WHISPER_VERSION} ${WHISPER_SHA} "${WHISPER_MODELS}" ${ONNX_VERSION} ${ONNX_SHA}

FROM base AS runner
ARG ARCH
ARG WHISPER_VERSION
ARG ONNX_VERSION
COPY --from=builder /src/dist/calls-transcriber-linux-${ARCH:?} /opt/calls-transcriber/bin/calls-transcriber
COPY --from=builder /tmp/whisper.cpp-${WHISPER_VERSION}/models /models
COPY --from=builder /tmp/onnxruntime-linux-x64-${ONNX_VERSION}/lib/* /usr/lib/
COPY --from=builder /src/build/models/silero_vad.onnx /models
COPY --from=builder /src/build/entrypoint.sh .
RUN ldconfig

ENTRYPOINT ["./entrypoint.sh"]
CMD ["/opt/calls-transcriber/bin/calls-transcriber"]
