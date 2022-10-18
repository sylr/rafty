# vi: ft=Dockerfile:

ARG GO_VERSION=1.18

FROM --platform=$TARGETPLATFORM golang:$GO_VERSION AS builder

ARG TARGETPLATFORM
ARG TARGETOS
ARG TARGETARCH
ARG TARGETVARIANT

ARG GIT_REVISION
ARG GIT_VERSION
ARG GO_BUILD_EXTLDFLAGS
ARG GO_BUILD_LDFLAGS_OPTIMS
ARG GO_BUILD_TAGS

RUN apt-get update && apt-get dist-upgrade -y && apt-get install -y build-essential git

WORKDIR $GOPATH/src/sylr.dev/rafty/examples/raftymcraftface

COPY go.mod go.sum ./

RUN go mod download

COPY . .

# Switch shell to bash
SHELL ["bash", "-c"]

RUN make build \
    GIT_REVISION=${GIT_REVISION} \
    GIT_VERSION=${GIT_VERSION} \
    GO_BUILD_EXTLDFLAGS="${GO_BUILD_EXTLDFLAGS}" \
    GO_BUILD_LDFLAGS_OPTIMS="${GO_BUILD_LDFLAGS_OPTIMS}" \
    GO_BUILD_TARGET=dist/${TARGETPLATFORM}/raftymcraftface \
    GO_BUILD_TAGS=${GO_BUILD_TAGS} \
    GOOS=${TARGETOS} \
    GOARCH=${TARGETARCH} \
    GOARM=${TARGETVARIANT/v/}

RUN useradd raftymcraftface

USER raftymcraftface:raftymcraftface

# -----------------------------------------------------------------------------

FROM scratch

ARG TARGETPLATFORM

WORKDIR /usr/local/bin

COPY --from=builder /usr/share/zoneinfo /usr/share/zoneinfo
COPY --from=builder /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/
COPY --from=builder /etc/passwd /etc/passwd
COPY --from=builder /etc/group /etc/group
COPY --from=builder /etc/services /etc/services
COPY --from=builder "/go/src/sylr.dev/rafty/examples/raftymcraftface/dist/$TARGETPLATFORM/raftymcraftface" .

USER raftymcraftface:raftymcraftface

CMD ["/usr/local/bin/raftymcraftface"]
