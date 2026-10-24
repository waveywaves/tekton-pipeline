ARG GO_BUILDER=registry.redhat.io/ubi9/go-toolset:latest@sha256:edb9452603fa48e9882063f001ecd7d322add20f1564a4f05f38f6683be5ea94
ARG RUNTIME=registry.access.redhat.com/ubi9/ubi-minimal:latest
# Add FIPS compliance layer
ARG FIPS_BUILDER=registry.access.redhat.com/ubi9/ubi-minimal@sha256:2d4c4551af6831dcd26bbf86108c4385de5183ec8fe6b5868f6f9640fe47f4ed
FROM $FIPS_BUILDER AS fips_builder

FROM $GO_BUILDER AS builder

WORKDIR /go/src/github.com/tektoncd/pipeline
COPY upstream .
COPY .konflux/patches patches/
RUN set -e; for f in patches/*.patch; do echo ${f}; [[ -f ${f} ]] || continue; git apply ${f}; done
COPY head HEAD
ENV GOEXPERIMENT=strictfipsruntime
ENV GODEBUG="http2server=0"
RUN go build -ldflags="-X 'knative.dev/pkg/changeset.rev=$(cat HEAD)'" -mod=vendor -tags disable_gcp,strictfipsruntime -v -o /tmp/nop \
    ./cmd/nop

FROM $RUNTIME
ARG VERSION=1.23

ENV NOP=/usr/local/bin/nop \
    KO_APP=/ko-app \
    KO_DATA_PATH=/kodata

COPY --from=builder /tmp/nop /ko-app/nop
COPY head ${KO_DATA_PATH}/HEAD

# Copy FIPS-compliant libraries
COPY --from=fips_builder /usr/lib64/libcrypto.so.3 /usr/lib64/
COPY --from=fips_builder /usr/lib64/libcrypto.so.3* /usr/lib64/
COPY --from=fips_builder /usr/lib64/ossl-modules/fips.so /usr/lib64/ossl-modules/
COPY --from=fips_builder /etc/redhat-release /etc/

LABEL \
    com.redhat.component="openshift-pipelines-nop-rhel9-container" \
    cpe="cpe:/a:redhat:openshift_pipelines:1.23::el9" \
    description="Red Hat OpenShift Pipelines tektoncd-pipeline nop" \
    distribution-scope="public" \
    io.k8s.description="Red Hat OpenShift Pipelines tektoncd-pipeline nop" \
    io.k8s.display-name="Red Hat OpenShift Pipelines tektoncd-pipeline nop" \
    io.openshift.tags="tekton,openshift,tektoncd-pipeline,nop" \
    maintainer="pipelines-extcomm@redhat.com" \
    name="openshift-pipelines/pipelines-nop-rhel9" \
    release="1227.1725849298" \
    summary="Red Hat OpenShift Pipelines tektoncd-pipeline nop" \
    url="https://access.redhat.com/containers/#/registry.access.redhat.com/ubi9-minimal/images/9.4-1227.1725849298" \
    vendor="Red Hat, Inc." \
    version="v1.23.3"

USER 65532

ENTRYPOINT ["/ko-app/nop"]
