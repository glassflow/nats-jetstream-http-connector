APP?=nats-jetstream-http-connector

GOVERSION?=1.21.4
CI_GOLANGCI_LINT_VERSION?=v1.55.2
GOLANGCI_LINT_VERSION?=latest


-include .env
include go.mk
