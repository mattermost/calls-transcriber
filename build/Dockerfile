# This dockerfile is used to build Mattermost calls-transcriber
# A multi stage build, with golang used as a builder
# and debian:bookworm-slim as runner
ARG ARCH
ARG GO_IMAGE
ARG RUNNER_IMAGE

FROM ${RUNNER_IMAGE:?} as base

# Setup system dependencies
WORKDIR /workdir

# Create unprivileged user to run the recorder process
RUN groupadd -r calls && useradd -mr -g calls calls

FROM ${GO_IMAGE:?} as builder
ARG ARCH
ARG OPUS_VERSION
ARG WHISPER_VERSION
ARG DEBIAN_FRONTEND=noninteractive
#GO_BUILD_PLATFORMS holds the platforms that we will build the docker image against
ARG GO_BUILD_PLATFORMS=linux-${ARCH:?}

# Setup directories structure and compile
COPY . /src
WORKDIR /src

RUN /bin/bash ./build/build.sh ${OPUS_VERSION} ${WHISPER_VERSION} "tiny base small"

FROM base AS runner
ARG ARCH
ARG WHISPER_VERSION
COPY --from=builder /src/dist/calls-transcriber-linux-${ARCH:?} /opt/calls-transcriber/bin/calls-transcriber
COPY --from=builder /tmp/whisper.cpp-${WHISPER_VERSION}/models /models

USER calls

ENTRYPOINT ["/opt/calls-transcriber/bin/calls-transcriber"]
