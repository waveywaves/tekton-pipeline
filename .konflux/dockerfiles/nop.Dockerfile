ARG GO_BUILDER=brew.registry.redhat.io/rh-osbs/openshift-golang-builder:v1.24
ARG RUNTIME=registry.access.redhat.com/ubi9/ubi-minimal:latest

FROM $GO_BUILDER AS builder

WORKDIR /go/src/github.com/tektoncd/pipeline
COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /go/bin/nop ./cmd/nop

FROM $RUNTIME

COPY --from=builder /go/bin/nop /ko-app/nop

ENTRYPOINT ["/ko-app/nop"]
