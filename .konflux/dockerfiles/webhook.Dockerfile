# Rebuild trigger: 1.15.4 release 2026-01-19
ARG GO_BUILDER=registry.access.redhat.com/ubi8/go-toolset:latest@sha256:f10be871f461b5b2920559e55cf3e594dc1758941b835b7a62b6bd1f08464181
ARG RUNTIME=registry.access.redhat.com/ubi8/ubi:latest

FROM $GO_BUILDER AS builder

WORKDIR /go/src/github.com/tektoncd/pipeline
COPY --chown=1001:0 upstream .
COPY --chown=1001:0 .konflux/patches patches/
RUN set -e; for f in patches/*.patch; do echo ${f}; [[ -f ${f} ]] || continue; git apply ${f}; done
COPY head HEAD
ENV GODEBUG="http2server=0"
RUN go build -ldflags="-X 'knative.dev/pkg/changeset.rev=$(cat HEAD)'" -mod=vendor -tags disable_gcp -v -o /tmp/webhook \
    ./cmd/webhook

FROM $RUNTIME
ARG VERSION=1.15

ENV WEBHOOK=/usr/local/bin/webhook \
    KO_APP=/ko-app \
    KO_DATA_PATH=/kodata

COPY --from=builder /tmp/webhook /ko-app/webhook
COPY head ${KO_DATA_PATH}/HEAD

LABEL \
    com.redhat.component="openshift-pipelines-webhook-rhel8-container" \
    cpe="cpe:/a:redhat:openshift_pipelines:1.15::el8" \
    description="Red Hat OpenShift Pipelines tektoncd-pipeline webhook" \
    io.k8s.description="Red Hat OpenShift Pipelines tektoncd-pipeline webhook" \
    io.k8s.display-name="Red Hat OpenShift Pipelines tektoncd-pipeline webhook" \
    io.openshift.tags="tekton,openshift,tektoncd-pipeline,webhook" \
    maintainer="pipelines-extcomm@redhat.com" \
    name="openshift-pipelines/pipelines-webhook-rhel8" \
    summary="Red Hat OpenShift Pipelines tektoncd-pipeline webhook" \
    version="v1.15.6"

RUN groupadd -r -g 65532 nonroot && useradd --no-log-init -r -u 65532 -g nonroot nonroot
USER 65532

ENTRYPOINT ["/ko-app/webhook"]
# trigger rebuild 2026-02-14
