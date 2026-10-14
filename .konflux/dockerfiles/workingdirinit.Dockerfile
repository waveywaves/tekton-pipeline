# Rebuild trigger: 1.15.4 release 2026-01-19
ARG GO_BUILDER=registry.access.redhat.com/ubi8/go-toolset:latest@sha256:c756ec2384eb3f7c03b4e7c79040e44352a775477981a1ba8640d5472434b39e
ARG RUNTIME=registry.access.redhat.com/ubi8/ubi:latest

FROM $GO_BUILDER AS builder

WORKDIR /go/src/github.com/tektoncd/pipeline
COPY --chown=1001:0 upstream .
COPY --chown=1001:0 .konflux/patches patches/
RUN set -e; for f in patches/*.patch; do echo ${f}; [[ -f ${f} ]] || continue; git apply ${f}; done
COPY head HEAD
ENV GODEBUG="http2server=0"
RUN go build -ldflags="-X 'knative.dev/pkg/changeset.rev=$(cat HEAD)'" -mod=vendor -tags disable_gcp -v -o /tmp/workingdirinit \
    ./cmd/workingdirinit

FROM $RUNTIME
ARG VERSION=1.15

ENV WORKINGDIRINIT=/usr/local/bin/workingdirinit \
    KO_APP=/ko-app \
    KO_DATA_PATH=/kodata

COPY --from=builder /tmp/workingdirinit /ko-app/workingdirinit
COPY head ${KO_DATA_PATH}/HEAD

LABEL \
    com.redhat.component="openshift-pipelines-workingdirinit-rhel8-container" \
    cpe="cpe:/a:redhat:openshift_pipelines:1.15::el8" \
    description="Red Hat OpenShift Pipelines tektoncd-pipeline workingdirinit" \
    io.k8s.description="Red Hat OpenShift Pipelines tektoncd-pipeline workingdirinit" \
    io.k8s.display-name="Red Hat OpenShift Pipelines tektoncd-pipeline workingdirinit" \
    io.openshift.tags="tekton,openshift,tektoncd-pipeline,workingdirinit" \
    maintainer="pipelines-extcomm@redhat.com" \
    name="openshift-pipelines/pipelines-workingdirinit-rhel8" \
    summary="Red Hat OpenShift Pipelines tektoncd-pipeline workingdirinit" \
    version="v1.15.6"

RUN groupadd -r -g 65532 nonroot && useradd --no-log-init -r -u 65532 -g nonroot nonroot
USER 65532

ENTRYPOINT ["/ko-app/workingdirinit"]
# trigger rebuild 2026-02-14
