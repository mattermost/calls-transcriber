# ====================================================================================
# Variables

## General Variables
# Branch Variables
PROTECTED_BRANCH := master
CURRENT_BRANCH   := $(shell git rev-parse --abbrev-ref HEAD)
# Use repository name as application name
APP_NAME    := $(shell basename -s .git `git config --get remote.origin.url`)
# Get current commit
APP_COMMIT  := $(shell git log --pretty=format:'%h' -n 1)
# Check if we are in protected branch, if yes use `protected_branch_name-sha` as app version.
# Else check if we are in a release tag, if yes use the tag as app version, else use `dev-sha` as app version.
APP_VERSION ?= $(shell if [ $(PROTECTED_BRANCH) = $(CURRENT_BRANCH) ]; then echo $(PROTECTED_BRANCH); else (git describe --abbrev=0 --exact-match --tags 2>/dev/null || echo dev-$(APP_COMMIT)) ; fi)

# Get current date and format like: 2022-04-27 11:32
BUILD_DATE  := $(shell date +%Y-%m-%d\ %H:%M)

## General Configuration Variables
# We don't need make's built-in rules.
MAKEFLAGS     += --no-builtin-rules
# Be pedantic about undefined variables.
MAKEFLAGS     += --warn-undefined-variables
# Set help as default target
.DEFAULT_GOAL := help

# App Code location
CONFIG_APP_CODE         += ./cmd/transcriber

# Operating system arch
ifneq (, $(shell which go))
ARCH                    ?= $(shell go version | awk '{print substr($$4,index($$4,"/")+1)}')
endif
# Fallback to amd64 if ARCH is still unset.
ARCH                    ?= amd64

## CGO dependencies
# Whisper.cpp
WHISPER_VERSION ?= "1.4.0"
WHISPER_SHA ?= "b2e34e65777033584fa6769a366cdb0228bc5c7da81e58a5e8dc0ce94d0fb54e"
WHISPER_MODELS ?= "tiny base small"
# Opus
OPUS_VERSION ?= "1.4"
OPUS_SHA ?= "c9b32b4253be5ae63d1ff16eea06b94b5f0f2951b7a02aceef58e3a3ce49c51f"
# ONNX Runtime
ONNX_VERSION ?= "1.16.2"
ONNX_SHA ?= "70c769771ad4b6d63b87ca1f62d3f11e998ea0b9d738d6bbdd6a5e6d8c1deb31"

## Docker Variables
# Docker executable
DOCKER                  := $(shell which docker)
# Dockerfile's location
DOCKER_FILE             += ./build/Dockerfile
# Docker options to inherit for all docker run commands
DOCKER_OPTS             += --rm -u $$(id -u):$$(id -g) --platform "linux/amd64"
# Registry to upload images
DOCKER_REGISTRY         ?= docker.io
DOCKER_REGISTRY_REPO    ?= mattermost/${APP_NAME}-daily
# Registry credentials
DOCKER_USER             ?= user
DOCKER_PASSWORD         ?= password
## Docker Images
DOCKER_IMAGE_GO         += "golang:${GO_VERSION}@sha256:337543447173c2238c78d4851456760dcc57c1dfa8c3bcd94cbee8b0f7b32ad0"
DOCKER_IMAGE_GOLINT     += "golangci/golangci-lint:v1.54.2@sha256:abe731fe6bb335a30eab303a41dd5c2b630bb174372a4da08e3d42eab5324127"
DOCKER_IMAGE_DOCKERLINT += "hadolint/hadolint:v2.9.2@sha256:d355bd7df747a0f124f3b5e7b21e9dafd0cb19732a276f901f0fdee243ec1f3b"
DOCKER_IMAGE_COSIGN     += "bitnami/cosign:1.8.0@sha256:8c2c61c546258fffff18b47bb82a65af6142007306b737129a7bd5429d53629a"
DOCKER_IMAGE_GH_CLI     += "ghcr.io/supportpal/github-gh-cli:2.31.0@sha256:71371e36e62bd24ddd42d9e4c720a7e9954cb599475e24d1407af7190e2a5685"

DOCKER_IMAGE_RUNNER     := "debian:bookworm-slim@sha256:5bbfcb9f36a506f9c9c2fb53205f15f6e9d1f0e032939378ddc049a2d26d651e"
ifeq ($(ARCH),arm64)
	DOCKER_IMAGE_RUNNER="arm64v8/debian:bookworm-slim@sha256:593234c0624826b26d7f7f807456dfc615c4d0f748a3ac410fbaf31a0b1d32ff"
endif

## Cosign Variables
# The public key
COSIGN_PUBLIC_KEY       ?= akey
# The private key
COSIGN_KEY              ?= akey
# The passphrase used to decrypt the private key
COSIGN_PASSWORD         ?= password

## Go Variables
# Go executable
GO                           := $(shell which go)
# Extract GO version from go.mod file
GO_VERSION                   ?= $(shell grep -E '^go' go.mod | awk {'print $$2'})
# LDFLAGS
GO_LDFLAGS                   += -X "github.com/mattermost/${APP_NAME}/service.buildHash=$(APP_COMMIT)"
GO_LDFLAGS                   += -X "github.com/mattermost/${APP_NAME}/service.buildVersion=$(APP_VERSION)"
GO_LDFLAGS                   += -X "github.com/mattermost/${APP_NAME}/service.buildDate=$(BUILD_DATE)"
GO_LDFLAGS                   += -X "github.com/mattermost/${APP_NAME}/service.goVersion=$(GO_VERSION)"
# Architectures to build for
GO_BUILD_PLATFORMS           ?= linux-amd64
GO_BUILD_PLATFORMS_ARTIFACTS = $(foreach cmd,$(addprefix go-build/,${APP_NAME}),$(addprefix $(cmd)-,$(GO_BUILD_PLATFORMS)))
# Build options
GO_BUILD_OPTS                += -mod=readonly -trimpath -buildmode=pie
GO_TEST_OPTS                 += -mod=readonly -failfast -race
# Temporary folder to output compiled binaries artifacts
GO_OUT_BIN_DIR               := ./dist

## Github Variables
# A github access token that provides access to upload artifacts under releases
GITHUB_TOKEN                 ?= a_token
# Github organization
GITHUB_ORG                   := mattermost
# Most probably the name of the repo
GITHUB_REPO                  := ${APP_NAME}

# ====================================================================================
# Colors

BLUE   := $(shell printf "\033[34m")
YELLOW := $(shell printf "\033[33m")
RED    := $(shell printf "\033[31m")
GREEN  := $(shell printf "\033[32m")
CYAN   := $(shell printf "\033[36m")
CNone  := $(shell printf "\033[0m")

# ====================================================================================
# Logger

TIME_LONG	= `date +%Y-%m-%d' '%H:%M:%S`
TIME_SHORT	= `date +%H:%M:%S`
TIME		= $(TIME_SHORT)

INFO = echo ${TIME} ${BLUE}[ .. ]${CNone}
WARN = echo ${TIME} ${YELLOW}[WARN]${CNone}
ERR  = echo ${TIME} ${RED}[FAIL]${CNone}
OK   = echo ${TIME} ${GREEN}[ OK ]${CNone}
FAIL = (echo ${TIME} ${RED}[FAIL]${CNone} && false)

# ====================================================================================
# Verbosity control hack

VERBOSE ?= 0
AT_0 := @
AT_1 :=
AT = $(AT_$(VERBOSE))

# ====================================================================================
# Targets

help: ## to get help
	@echo "Usage:"
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) |\
	awk 'BEGIN {FS = ":.*?## "}; {printf "make ${CYAN}%-30s${CNone} %s\n", $$1, $$2}'

.PHONY: build
build: go-build-docker ## to build

.PHONY: release
release: build github-release ## to build and release artifacts

.PHONY: package
package: docker-login docker-build docker-push ## to build, package and push the artifact to a container registry

.PHONY: sign
sign: docker-sign docker-verify ## to sign the artifact and perform verification

.PHONY: lint
lint: go-lint ## to lint

.PHONY: test
test: go-test ## to test

.PHONY: docker-build
docker-build: ## to build the docker image
	@$(INFO) Performing Docker build ${APP_NAME}:${APP_VERSION} for ${ARCH}
	$(AT)$(DOCKER) build \
	--build-arg GO_IMAGE=${DOCKER_IMAGE_GO} \
	--build-arg ARCH=${ARCH} \
	--build-arg RUNNER_IMAGE=${DOCKER_IMAGE_RUNNER} \
	--build-arg OPUS_VERSION=${OPUS_VERSION} \
	--build-arg OPUS_SHA=${OPUS_SHA} \
	--build-arg WHISPER_VERSION=${WHISPER_VERSION} \
	--build-arg WHISPER_SHA=${WHISPER_SHA} \
	--build-arg WHISPER_MODELS=${WHISPER_MODELS} \
	--build-arg ONNX_VERSION=${ONNX_VERSION} \
	--build-arg ONNX_SHA=${ONNX_SHA} \
	-f ${DOCKER_FILE} . \
	-t ${APP_NAME}:${APP_VERSION} || ${FAIL}
	@$(OK) Performing Docker build ${APP_NAME}:${APP_VERSION}

.PHONY: docker-push
docker-push: ## to push the docker image
	@$(INFO) Pushing to registry...
	$(AT)$(DOCKER) tag ${APP_NAME}:${APP_VERSION} $(DOCKER_REGISTRY)/${DOCKER_REGISTRY_REPO}:${APP_VERSION} || ${FAIL}
	$(AT)$(DOCKER) push $(DOCKER_REGISTRY)/${DOCKER_REGISTRY_REPO}:${APP_VERSION} || ${FAIL}
# if we are on a latest semver APP_VERSION tag, also push latest
ifneq ($(shell echo $(APP_VERSION) | egrep '^v([0-9]+\.){0,2}(\*|[0-9]+)'),)
  ifeq ($(shell git tag -l --sort=v:refname | tail -n1),$(APP_VERSION))
	$(AT)$(DOCKER) tag ${APP_NAME}:${APP_VERSION} $(DOCKER_REGISTRY)/${DOCKER_REGISTRY_REPO}:latest || ${FAIL}
	$(AT)$(DOCKER) push $(DOCKER_REGISTRY)/${DOCKER_REGISTRY_REPO}:latest || ${FAIL}
  endif
endif
	@$(OK) Pushing to registry $(DOCKER_REGISTRY)/${DOCKER_REGISTRY_REPO}:${APP_VERSION}

.PHONY: docker-sign
docker-sign: ## to sign the docker image
	@$(INFO) Signing the docker image...
	$(AT)echo "$${COSIGN_KEY}" > cosign.key && \
	$(DOCKER) run ${DOCKER_OPTS} \
	--entrypoint '/bin/sh' \
        -v $(PWD):/app -w /app \
	-e COSIGN_PASSWORD=${COSIGN_PASSWORD} \
	-e HOME="/tmp" \
    ${DOCKER_IMAGE_COSIGN} \
	-c \
	"echo Signing... && \
	cosign login $(DOCKER_REGISTRY) -u ${DOCKER_USER} -p ${DOCKER_PASSWORD} && \
	cosign sign --key cosign.key $(DOCKER_REGISTRY)/${DOCKER_REGISTRY_REPO}:${APP_VERSION}" || ${FAIL}
# if we are on a latest semver APP_VERSION tag, also sign latest tag
ifneq ($(shell echo $(APP_VERSION) | egrep '^v([0-9]+\.){0,2}(\*|[0-9]+)'),)
  ifeq ($(shell git tag -l --sort=v:refname | tail -n1),$(APP_VERSION))
	$(DOCKER) run ${DOCKER_OPTS} \
	--entrypoint '/bin/sh' \
        -v $(PWD):/app -w /app \
	-e COSIGN_PASSWORD=${COSIGN_PASSWORD} \
	-e HOME="/tmp" \
	${DOCKER_IMAGE_COSIGN} \
	-c \
	"echo Signing... && \
	cosign login $(DOCKER_REGISTRY) -u ${DOCKER_USER} -p ${DOCKER_PASSWORD} && \
	cosign sign --key cosign.key $(DOCKER_REGISTRY)/${DOCKER_REGISTRY_REPO}:latest" || ${FAIL}
  endif
endif
	$(AT)rm -f cosign.key || ${FAIL}
	@$(OK) Signing the docker image: $(DOCKER_REGISTRY)/${DOCKER_REGISTRY_REPO}:${APP_VERSION}

.PHONY: docker-verify
docker-verify: ## to verify the docker image
	@$(INFO) Verifying the published docker image...
	$(AT)echo "$${COSIGN_PUBLIC_KEY}" > cosign_public.key && \
	$(DOCKER) run ${DOCKER_OPTS} \
	--entrypoint '/bin/sh' \
	-v $(PWD):/app -w /app \
	${DOCKER_IMAGE_COSIGN} \
	-c \
	"echo Verifying... && \
	cosign verify --key cosign_public.key $(DOCKER_REGISTRY)/${DOCKER_REGISTRY_REPO}:${APP_VERSION}" || ${FAIL}
# if we are on a latest semver APP_VERSION tag, also verify latest tag
ifneq ($(shell echo $(APP_VERSION) | egrep '^v([0-9]+\.){0,2}(\*|[0-9]+)'),)
  ifeq ($(shell git tag -l --sort=v:refname | tail -n1),$(APP_VERSION))
	$(DOCKER) run ${DOCKER_OPTS} \
	--entrypoint '/bin/sh' \
	-v $(PWD):/app -w /app \
	${DOCKER_IMAGE_COSIGN} \
	-c \
	"echo Verifying... && \
	cosign verify --key cosign_public.key $(DOCKER_REGISTRY)/${DOCKER_REGISTRY_REPO}:latest" || ${FAIL}
  endif
endif
	$(AT)rm -f cosign_public.key || ${FAIL}
	@$(OK) Verifying the published docker image: $(DOCKER_REGISTRY)/${DOCKER_REGISTRY_REPO}:${APP_VERSION}

.PHONY: docker-sbom
docker-sbom: ## to print a sbom report
	@$(INFO) Performing Docker sbom report...
	$(AT)$(DOCKER) sbom ${APP_NAME}:${APP_VERSION} || ${FAIL}
	@$(OK) Performing Docker sbom report

.PHONY: docker-scan
docker-scan: ## to print a vulnerability report
	@$(INFO) Performing Docker scan report...
	$(AT)$(DOCKER) scan ${APP_NAME}:${APP_VERSION} || ${FAIL}
	@$(OK) Performing Docker scan report

.PHONY: docker-lint
docker-lint: ## to lint the Dockerfile
	@$(INFO) Dockerfile linting...
	$(AT)$(DOCKER) run -i ${DOCKER_OPTS} \
	${DOCKER_IMAGE_DOCKERLINT} \
	< ${DOCKER_FILE} || ${FAIL}
	@$(OK) Dockerfile linting

.PHONY: docker-login
docker-login: ## to login to a container registry
	@$(INFO) Dockerd login to container registry ${DOCKER_REGISTRY}...
	$(AT) echo "${DOCKER_PASSWORD}" | $(DOCKER) login --password-stdin -u ${DOCKER_USER} $(DOCKER_REGISTRY) || ${FAIL}
	@$(OK) Dockerd login to container registry ${DOCKER_REGISTRY}...

go-build: $(GO_BUILD_PLATFORMS_ARTIFACTS) ## to build binaries

.PHONY: go-build
go-build/%:
	@$(INFO) go build $*...
	$(AT)target="$*"; \
	command="${APP_NAME}"; \
	platform_ext="$${target#$$command-*}"; \
	platform="$${platform_ext%.*}"; \
	export GOOS="$${platform%%-*}"; \
	export GOARCH="$${platform#*-}"; \
	echo export GOOS=$${GOOS}; \
	echo export GOARCH=$${GOARCH}; \
	$(GO) build ${GO_BUILD_OPTS} \
	-ldflags '${GO_LDFLAGS}' \
	-o ${GO_OUT_BIN_DIR}/$* \
	${CONFIG_APP_CODE} || ${FAIL}
	@$(OK) go build $*

.PHONY: go-build-docker
go-build-docker: # to build binaries under a controlled docker dedicated go container using DOCKER_IMAGE_GO
	@$(INFO) go build docker
	$(AT)$(DOCKER) run ${DOCKER_OPTS} \
	-v $(PWD):/app -w /app \
	-e GOCACHE="/tmp" \
	$(DOCKER_IMAGE_GO) \
	/bin/bash ./build/build.sh ${OPUS_VERSION} ${OPUS_SHA} ${WHISPER_VERSION} ${WHISPER_SHA} ${WHISPER_MODELS} ${ONNX_VERSION} ${ONNX_SHA} || ${FAIL}
	@$(OK) go build docker

.PHONY: go-run
go-run: ## to run locally for development
	@$(INFO) running locally...
	$(AT)$(GO) run ${GO_BUILD_OPTS} ${CONFIG_APP_CODE} || ${FAIL}
	@$(OK) running locally

.PHONY: go-test
go-test: ## to run tests
	@$(INFO) testing...
	$(AT)$(DOCKER) run ${DOCKER_OPTS} \
	-v $(PWD):/app -w /app \
	-v /var/run/docker.sock:/var/run/docker.sock \
	-e GOCACHE="/tmp" \
	$(DOCKER_IMAGE_GO) \
	/bin/sh ./build/run_tests.sh "${GO_TEST_OPTS}" "${OPUS_VERSION}" "${OPUS_SHA}" "${WHISPER_VERSION}" "${WHISPER_SHA}" "${ONNX_VERSION}" "${ONNX_SHA}" || ${FAIL}
	@$(OK) testing

.PHONY: go-mod-check
go-mod-check: ## to check go mod files consistency
	@$(INFO) Checking go mod files consistency...
	$(AT)$(GO) mod tidy
	$(AT)git --no-pager diff --exit-code go.mod go.sum || \
	(${WARN} Please run "go mod tidy" and commit the changes in go.mod and go.sum. && ${FAIL} ; exit 128 )
	@$(OK) Checking go mod files consistency

.PHONY: go-update-dependencies
go-update-dependencies: ## to update go dependencies (vendor)
	@$(INFO) updating go dependencies...
	$(AT)$(GO) get -u ./... && \
	$(AT)$(GO) mod vendor && \
	$(AT)$(GO) mod tidy || ${FAIL}
	@$(OK) updating go dependencies

.PHONY: go-lint
go-lint: ## to lint go code
	@$(INFO) App linting...
	$(AT)GOCACHE="/tmp" $(DOCKER) run ${DOCKER_OPTS} \
	-v $(PWD):/app -w /app \
	-e GOCACHE="/tmp" \
	-e GOLANGCI_LINT_CACHE="/tmp" \
	${DOCKER_IMAGE_GOLINT} \
	/bin/sh ./build/lint.sh "${OPUS_VERSION}" "${OPUS_SHA}" "${WHISPER_VERSION}" "${WHISPER_SHA}" "${ONNX_VERSION}" "${ONNX_SHA}" || ${FAIL}
	@$(OK) App linting

.PHONY: go-fmt
go-fmt: ## to perform formatting
	@$(INFO) App code formatting...
	$(AT)$(GO) fmt ./... || ${FAIL}
	@$(OK) App code formatting...

.PHONY: github-release
github-release: ## to publish a release and relevant artifacts to GitHub
	@$(INFO) Generating github-release http://github.com/$(GITHUB_ORG)/$(GITHUB_REPO)/releases/tag/$(APP_VERSION) ...
ifeq ($(shell echo $(APP_VERSION) | egrep '^v([0-9]+\.){0,2}(\*|[0-9]+)'),)
	$(error "We only support releases from semver tags")
else
	$(AT)$(DOCKER) run \
	-v $(PWD):/app -w /app \
	-e GITHUB_TOKEN=${GITHUB_TOKEN} \
	$(DOCKER_IMAGE_GH_CLI) \
	/bin/sh -c \
	"git config --global --add safe.directory /app && cd /app && \
	gh release create $(APP_VERSION) --generate-notes $(GO_OUT_BIN_DIR)/*" || ${FAIL}
endif
	@$(OK) Generating github-release http://github.com/$(GITHUB_ORG)/$(GITHUB_REPO)/releases/tag/$(APP_VERSION) ...

.PHONY: clean
clean: ## to clean-up
	@$(INFO) cleaning /${GO_OUT_BIN_DIR} folder...
	$(AT)rm -rf ${GO_OUT_BIN_DIR} || ${FAIL}
	@$(OK) cleaning /${GO_OUT_BIN_DIR} folder
