# Rebuild trigger: 1.15.4 release 2026-01-19
ARG GO_BUILDER=registry.access.redhat.com/ubi8/go-toolset:latest@sha256:7b04f42022fe4b89a08e5737fe8b289e329209d2c69a43188d6b9c76de92b558
ARG RUNTIME=registry.access.redhat.com/ubi8/ubi:latest

FROM $GO_BUILDER AS builder

WORKDIR /go/src/github.com/tektoncd/pipeline
COPY --chown=1001:0 upstream .
COPY --chown=1001:0 .konflux/patches patches/
RUN set -e; for f in patches/*.patch; do echo ${f}; [[ -f ${f} ]] || continue; git apply ${f}; done
COPY head HEAD
ENV CHANGESET_REV=$CI_PIPELINE_UPSTREAM_COMMIT
ENV GODEBUG="http2server=0"
RUN CGO_ENABLED=0 \
    go build -ldflags="-X 'knative.dev/pkg/changeset.rev=$(cat HEAD)'" -mod=vendor -tags disable_gcp -v -o /tmp/entrypoint \
    ./cmd/entrypoint

FROM $RUNTIME
ARG VERSION=1.15

ENV ENTRYPOINT=/usr/local/bin/entrypoint \
    KO_APP=/ko-app \
    KO_DATA_PATH=/kodata

COPY --from=builder /tmp/entrypoint /ko-app/entrypoint
COPY head ${KO_DATA_PATH}/HEAD

LABEL \
    com.redhat.component="openshift-pipelines-entrypoint-rhel8-container" \
    cpe="cpe:/a:redhat:openshift_pipelines:1.15::el8" \
    description="Red Hat OpenShift Pipelines tektoncd-pipeline entrypoint" \
    io.k8s.description="Red Hat OpenShift Pipelines tektoncd-pipeline entrypoint" \
    io.k8s.display-name="Red Hat OpenShift Pipelines tektoncd-pipeline entrypoint" \
    io.openshift.tags="tekton,openshift,tektoncd-pipeline,entrypoint" \
    maintainer="pipelines-extcomm@redhat.com" \
    name="openshift-pipelines/pipelines-entrypoint-rhel8" \
    summary="Red Hat OpenShift Pipelines tektoncd-pipeline entrypoint" \
    version="v1.15.6"

RUN groupadd -r -g 65532 nonroot && useradd --no-log-init -r -u 65532 -g nonroot nonroot
USER 65532

ENTRYPOINT ["/ko-app/entrypoint"]
# trigger rebuild 2026-02-14
