# Rebuild trigger: 1.15.4 release 2026-01-19
ARG GO_BUILDER=registry.access.redhat.com/ubi8/go-toolset:latest@sha256:7b04f42022fe4b89a08e5737fe8b289e329209d2c69a43188d6b9c76de92b558
ARG RUNTIME=scratch

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
RUN go build -ldflags="-X 'knative.dev/pkg/changeset.rev=$(cat HEAD)'" -mod=vendor -tags disable_gcp -v -o /tmp/nop \
    ./cmd/nop

FROM $RUNTIME
ARG VERSION=1.15

ENV NOP=/usr/local/bin/nop \
    KO_APP=/ko-app \
    KO_DATA_PATH=/kodata

COPY --from=builder /tmp/nop /ko-app/nop
COPY head ${KO_DATA_PATH}/HEAD

LABEL \
    com.redhat.component="openshift-pipelines-nop-rhel8-container" \
    cpe="cpe:/a:redhat:openshift_pipelines:1.15::el8" \
    description="Red Hat OpenShift Pipelines tektoncd-pipeline nop" \
    distribution-scope="public" \
    io.k8s.description="Red Hat OpenShift Pipelines tektoncd-pipeline nop" \
    io.k8s.display-name="Red Hat OpenShift Pipelines tektoncd-pipeline nop" \
    io.openshift.tags="tekton,openshift,tektoncd-pipeline,nop" \
    maintainer="pipelines-extcomm@redhat.com" \
    name="openshift-pipelines/pipelines-nop-rhel8" \
    release="1227.1725849298" \
    summary="Red Hat OpenShift Pipelines tektoncd-pipeline nop" \
    url="https://access.redhat.com/containers/#/registry.access.redhat.com/ubi9-minimal/images/9.4-1227.1725849298" \
    vendor="Red Hat, Inc." \
    version="v1.15.6"

USER 65532

ENTRYPOINT ["/ko-app/nop"]