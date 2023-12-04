APP?=
VERSION?=$(shell git describe --tag --always --dirty)
COMMIT_HASH?=$(shell git rev-parse --short HEAD)

GOVERSION?=
CI_GOLANGCI_LINT_VERSION?=
GOLANGCI_LINT_VERSION?=latest
CODEGEN_PATH?=

GOOGLE_APPLICATION_CREDENTIALS=${HOME}/.config/gcloud/application_default_credentials.json


-include .env


# === CI steps begin ===
ci-check: check-go-mod-vendor codegen-install check-codegen check-readme
	$(call check-git-diff-is-clean,CI detects uncommited changes)

package: VERSION=$(if ${TAG_NAME},${TAG_NAME},${COMMIT_HASH})
package: version docker-build
	docker push ${DOCKER_IMAGE}
ifneq (${TAG_NAME},) # if TAG_NAME is not empty - tag is pushed
	docker tag ${DOCKER_IMAGE} ${DOCKER_IMAGE_NAME}:${COMMIT_HASH}
	docker push ${DOCKER_IMAGE_NAME}:${COMMIT_HASH}
endif
ifneq (,$(filter ${BRANCH_NAME},master main)) # if BRANCH_NAME is master|main
	docker tag ${DOCKER_IMAGE} ${DOCKER_IMAGE_NAME}:latest
	docker push ${DOCKER_IMAGE_NAME}:latest
endif
# === CI steps end ===


version:
	@ echo "VERSION:" ${VERSION}
	@ echo "COMMIT_HASH:" ${COMMIT_HASH}
	@ echo "GOVERSION:" ${GOVERSION}

build: go-build

GO?=go
GO_MOD?=readonly
GO_BUILD_ENV_PREFIX?=GOWORK=off GOEXPERIMENT=loopvar
BUILD_OUTPUT?=/tmp/${APP}
GO_LDFLAGS_VERSION_COMMIT_PATH?=github.com/glassflow/${APP}/pkg/service

go-build:
	${GO_BUILD_ENV_PREFIX} ${GO} build -mod=${GO_MOD} -o ${BUILD_OUTPUT} \
		-ldflags "-X ${GO_LDFLAGS_VERSION_COMMIT_PATH}.version=${VERSION} -X ${GO_LDFLAGS_VERSION_COMMIT_PATH}.commit=${COMMIT_HASH}" \
		cmd/${APP}/main.go

test:
	${GO_BUILD_ENV_PREFIX} ${GO} test -mod=${GO_MOD} -cover -race ./...

lint: lint-golangci lint-nargs
lint-install: lint-golangci-install lint-nargs-install

lint-golangci-install:
	${GO} install github.com/golangci/golangci-lint/cmd/golangci-lint@${GOLANGCI_LINT_VERSION}

lint-golangci:
	GOWORK=off GOFLAGS=-mod=${GO_MOD} golangci-lint run ./... --timeout 5m

lint-nargs-install:
	GO111MODULE=off ${GO} get -u github.com/alexkohler/nargs/cmd/nargs

lint-nargs:
	GOPRIVATE=github.com/glassflow ${GO} list ./... | grep -v /${CODEGEN_PATH} | xargs -L1 nargs -receivers

lint-nilaway-install:
	go install go.uber.org/nilaway/cmd/nilaway@latest

lint-nilaway:
	nilaway ./...

ci-lint: GOLANGCI_LINT_VERSION=${CI_GOLANGCI_LINT_VERSION}
ci-lint: lint-install lint


pre-push-checks: go-mod-vendor codegen-gen build test lint update-readme
	$(call check-git-diff-is-clean,some checks detects uncommited changes)


DOCKER_REGISTRY?=
DOCKER_IMAGE_NAME?=$(if ${DOCKER_REGISTRY},${DOCKER_REGISTRY}/,)${APP}
DOCKER_IMAGE?=${DOCKER_IMAGE_NAME}:${VERSION}
DOCKER_IMAGE_LATEST?=${DOCKER_IMAGE_NAME}:latest
DOCKER_BUILD_PLATFORM?= # linux/amd64 # ,linux/arm64

# check if 'docker' command is exists
ifneq (,$(shell which docker))
# check if latest image exists
ifeq (0,$(shell docker image inspect ${DOCKER_IMAGE_LATEST} 2>/dev/null 1>&2; echo $$?))
	DOCKER_IMAGE_CACHE_FROM?=--cache-from ${DOCKER_IMAGE_LATEST}
endif
endif

docker-build:
	docker build -t ${DOCKER_IMAGE} \
		$(if ${DOCKER_BUILD_PLATFORM},--platform=${DOCKER_BUILD_PLATFORM},) \
		${DOCKER_IMAGE_CACHE_FROM} \
		--build-arg _VERSION=${VERSION} \
		$(if ${GOVERSION},--build-arg _GOVERSION=${GOVERSION},) \
		.
ifdef DOCKER_LATEST_UPDATE
	docker tag ${DOCKER_IMAGE} ${DOCKER_IMAGE_LATEST}
endif
ifdef DOCKER_PUSH
	docker push ${DOCKER_IMAGE}
endif

docker-build-amd64: DOCKER_BUILD_PLATFORM=linux/amd64
docker-build-amd64: docker-build

docker-run:
	docker run --rm -it \
		-e ENV=${ENV} \
		-e GOOGLE_APPLICATION_CREDENTIALS=/gcloud/application_default_credentials.json \
		-v ~/.config/gcloud:/gcloud \
		-p 9001:8080 ${DOCKER_IMAGE}


export_env_vars:
	$(eval export $(shell sed -ne 's/ *#.*$$//; /./ s/=.*$$// p' .env))

local-run: export_env_vars
	${GO} run cmd/${APP}/main.go

go-mod-vendor: update-go-mod-version
	${GO} mod tidy
ifeq (${GO_MOD},vendor)
	${GO} mod vendor
endif

check-go-mod-vendor: go-mod-vendor
	$(call check-git-diff-is-clean,go.mod file is outdated)

update-go-mod-version:
	${GO} mod edit -go=${GOVERSION}

readme-update: update-readme

README_GEN_BIN?=${GO} run cmd/${APP}/main.go

update-readme: TMP_FILE=readme_new.md
update-readme: START_LINE=\[cmd-output\]: \# \(PRINT HELP\)
update-readme: END_LINE=\[cmd-output\]: \# \(END\)
update-readme: START_LINE_TEXT=$(shell echo "${START_LINE}" | sed -r 's/\\//g')
update-readme: END_LINE_TEXT=$(shell echo "${END_LINE}" | sed -r 's/\\//g')
update-readme:
	@ grep -F -q "${START_LINE_TEXT}" README.md || (echo "README.md should contain line: ${START_LINE_TEXT}" && exit 1)
	@ grep -F -q "${END_LINE_TEXT}" README.md || (echo "README.md should contain line: ${END_LINE_TEXT}" && exit 1)
	cat README.md | sed -r '/${START_LINE}$$/q' > ${TMP_FILE}
	@ echo "" >> ${TMP_FILE}
	${README_GEN_BIN} -h >> ${TMP_FILE}
	@ echo "" >> ${TMP_FILE}
	cat README.md | sed -r -n '/${END_LINE}$$/,$$ p' >> ${TMP_FILE}
	@ cp -f ${TMP_FILE} README.md
	@ rm ${TMP_FILE}

check-readme: update-readme
	$(call check-git-diff-is-clean,README.md file is outdated)

codegen-gen:
ifneq (${CODEGEN_PATH},)
	goag --file openapi.yaml --out ${CODEGEN_PATH} -package goag
endif

codegen-install:
ifneq (${CODEGEN_PATH},)
	${GO} install github.com/vkd/goag/cmd/goag@latest
endif

check-codegen: codegen-gen
	$(call check-git-diff-is-clean,codegen tool 'goag' in path './${CODEGEN_PATH}')

next-tag:
	NEXT_TAG=$(shell echo ${VERSION} | awk -F. -v OFS=. '{$$NF += 1 ; print}') git tag ${NEXT_TAG}

env-%:
	@ echo ${$*}

define required-var
	@ [ "${$(1)}" ] || (echo "USAGE: 'make $(MAKECMDGOALS) $(1)=<value> $(MAKEFLAGS)': $(1) var is required"; exit 1)
endef

check-git-diff-is-clean:
	$(call check-git-diff-is-clean,)

define check-git-diff-is-clean
	@ git diff --exit-code || (echo "Error: git diff is not clean - $(1).\nTry to run 'make $@' locally and commit all necessary changes" && exit 2)
endef

define check-go-mod-version
	@ [[ "$(shell ${GO} mod edit -print | grep "^go .*")" == "go ${GOVERSION}" ]] || (echo "Error: go mod has different go-version - try to run 'make update-go-mod-version' locally and commit all necessary changes" && exit 2)
endef

sync:
	cp ../$(if ${FROM},${FROM},glassflow-api)/go.mk ./
