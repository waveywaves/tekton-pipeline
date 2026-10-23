ARG GO_BUILDER=registry.redhat.io/ubi9/go-toolset:latest@sha256:edb9452603fa48e9882063f001ecd7d322add20f1564a4f05f38f6683be5ea94
ARG RUNTIME=registry.access.redhat.com/ubi9/ubi-minimal:latest

FROM $GO_BUILDER AS builder

WORKDIR /go/src/github.com/tektoncd/pipeline
COPY upstream .
COPY .konflux/patches patches/
RUN set -e; for f in patches/*.patch; do echo ${f}; [[ -f ${f} ]] || continue; git apply ${f}; done
COPY head HEAD
ENV GOEXPERIMENT=strictfipsruntime
ENV GODEBUG="http2server=0"
RUN go build -ldflags="-X 'knative.dev/pkg/changeset.rev=$(cat HEAD)'" -mod=vendor -tags disable_gcp,strictfipsruntime -v -o /tmp/controller \
    ./cmd/controller

FROM $RUNTIME
ARG VERSION=1.23

ENV CONTROLLER=/usr/local/bin/controller \
    KO_APP=/ko-app \
    KO_DATA_PATH=/kodata

COPY --from=builder /tmp/controller /ko-app/controller
COPY head ${KO_DATA_PATH}/HEAD

LABEL \
    com.redhat.component="openshift-pipelines-controller-rhel9-container" \
    cpe="cpe:/a:redhat:openshift_pipelines:1.23::el9" \
    description="Red Hat OpenShift Pipelines tektoncd-pipeline controller" \
    io.k8s.description="Red Hat OpenShift Pipelines tektoncd-pipeline controller" \
    io.k8s.display-name="Red Hat OpenShift Pipelines tektoncd-pipeline controller" \
    io.openshift.tags="tekton,openshift,tektoncd-pipeline,controller" \
    maintainer="pipelines-extcomm@redhat.com" \
    name="openshift-pipelines/pipelines-controller-rhel9" \
    summary="Red Hat OpenShift Pipelines tektoncd-pipeline controller" \
    version="v1.23.3"

RUN groupadd -r -g 65532 nonroot && \
    useradd --no-log-init -r -u 65532 -g nonroot nonroot
USER 65532

ENTRYPOINT ["/ko-app/controller"]

