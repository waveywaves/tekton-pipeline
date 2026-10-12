# Rebuild trigger: 1.15.4 release 2026-01-19
ARG GO_BUILDER=registry.access.redhat.com/ubi8/go-toolset:latest@sha256:c756ec2384eb3f7c03b4e7c79040e44352a775477981a1ba8640d5472434b39e
ARG RUNTIME=registry.access.redhat.com/ubi8/ubi:latest

FROM $GO_BUILDER AS builder

WORKDIR /go/src/github.com/tektoncd/pipeline
COPY upstream .
COPY .konflux/patches patches/
# COPY leaves the source tree root-owned, so temporarily elevate to apply patches.
USER 0
RUN set -e; for f in patches/*.patch; do echo ${f}; [[ -f ${f} ]] || continue; git apply ${f}; done
USER 1001
COPY head HEAD
ENV GODEBUG="http2server=0"
RUN go build -ldflags="-X 'knative.dev/pkg/changeset.rev=$(cat HEAD)'" -mod=vendor -tags disable_gcp -v -o /tmp/controller \
    ./cmd/controller

FROM $RUNTIME
ARG VERSION=1.15

ENV CONTROLLER=/usr/local/bin/controller \
    KO_APP=/ko-app \
    KO_DATA_PATH=/kodata

COPY --from=builder /tmp/controller /ko-app/controller
COPY head ${KO_DATA_PATH}/HEAD

LABEL \
    com.redhat.component="openshift-pipelines-controller-rhel8-container" \
    cpe="cpe:/a:redhat:openshift_pipelines:1.15::el8" \
    description="Red Hat OpenShift Pipelines tektoncd-pipeline controller" \
    io.k8s.description="Red Hat OpenShift Pipelines tektoncd-pipeline controller" \
    io.k8s.display-name="Red Hat OpenShift Pipelines tektoncd-pipeline controller" \
    io.openshift.tags="tekton,openshift,tektoncd-pipeline,controller" \
    maintainer="pipelines-extcomm@redhat.com" \
    name="openshift-pipelines/pipelines-controller-rhel8" \
    summary="Red Hat OpenShift Pipelines tektoncd-pipeline controller" \
    version="v1.15.6"

RUN groupadd -r -g 65532 nonroot && useradd --no-log-init -r -u 65532 -g nonroot nonroot
USER 65532

ENTRYPOINT ["/ko-app/controller"]

# trigger rebuild 2026-02-14
