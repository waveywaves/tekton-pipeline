ARG GO_BUILDER=registry.access.redhat.com/ubi9/go-toolset:latest@sha256:d4d25f236c2b3cd27eb407e3b3478bef0b75d644ee6d11772217f537be926b11
ARG RUNTIME=registry.access.redhat.com/ubi9/ubi-minimal@sha256:c4c285d42941ebb2bd4da878479008e1e4536bb599aad1dbe568f89de9053661

FROM $GO_BUILDER AS builder

WORKDIR /go/src/github.com/tektoncd/pipeline
COPY upstream .
COPY .konflux/patches patches/
RUN set -e; for f in patches/*.patch; do echo ${f}; [[ -f ${f} ]] || continue; git apply ${f}; done
COPY head HEAD
ENV GOEXPERIMENT=strictfipsruntime
ENV GODEBUG="http2server=0"
RUN go build -ldflags="-X 'knative.dev/pkg/changeset.rev=$(cat HEAD)'" -mod=vendor -tags disable_gcp,strictfipsruntime -v -o /tmp/workingdirinit \
    ./cmd/workingdirinit

FROM $RUNTIME
ARG VERSION=1.24

ENV WORKINGDIRINIT=/usr/local/bin/workingdirinit \
    KO_APP=/ko-app \
    KO_DATA_PATH=/kodata

COPY --from=builder /tmp/workingdirinit /ko-app/workingdirinit
COPY head ${KO_DATA_PATH}/HEAD

LABEL \
    com.redhat.component="openshift-pipelines-workingdirinit-rhel9-container" \
    cpe="cpe:/a:redhat:openshift_pipelines:1.24::el9" \
    description="Red Hat OpenShift Pipelines tektoncd-pipeline workingdirinit" \
    io.k8s.description="Red Hat OpenShift Pipelines tektoncd-pipeline workingdirinit" \
    io.k8s.display-name="Red Hat OpenShift Pipelines tektoncd-pipeline workingdirinit" \
    io.openshift.tags="tekton,openshift,tektoncd-pipeline,workingdirinit" \
    maintainer="pipelines-extcomm@redhat.com" \
    name="openshift-pipelines/pipelines-workingdirinit-rhel9" \
    summary="Red Hat OpenShift Pipelines tektoncd-pipeline workingdirinit" \
    version="v1.24.2"

RUN groupadd -r -g 65532 nonroot && \
    useradd --no-log-init -r -u 65532 -g nonroot nonroot
USER 65532

ENTRYPOINT ["/ko-app/workingdirinit"]
