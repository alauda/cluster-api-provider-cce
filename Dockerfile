ARG OPS_DISTROLESS_TAG=20220718

FROM build-harbor.alauda.cn/ait/go-builder:1.19-alpine AS builder

ARG BUILD_OPTS=""

ARG GOMAXPROCS="2"

WORKDIR /src

COPY . /src

RUN CGO_ENABLED=0 go build ${BUILD_OPTS} -buildmode=pie -ldflags '-extldflags "-Wl,-z,relro,-z,now" -linkmode=external' -a -o bin/manager cmd/main.go \
    && strip /src/bin/manager \
    && chmod +x /src/bin/manager

FROM build-harbor.alauda.cn/ops/distroless-static-nonroot:${OPS_DISTROLESS_TAG}

ARG ALAUDA_UID="697"
ARG ALAUDA_GID="697"

LABEL OPS_DISTROLESS_TAG="${OPS_DISTROLESS_TAG}"
USER ${ALAUDA_UID}:${ALAUDA_GID}

COPY --from=builder /src/bin/manager /
WORKDIR /

ENTRYPOINT ["/manager"]
