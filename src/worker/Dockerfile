# Build context is the src/ directory:  docker build -f worker/Dockerfile src
# The build stage always runs on the build host's native platform and cross-compiles,
# so multi-arch images do not need QEMU emulation.
FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS build
ARG TARGETOS
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY api ./api
COPY worker ./worker
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w" -o /out/app ./worker

# distroless/static ships CA certificates, which the AWS SDK needs for TLS.
FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/app /app
USER nonroot:nonroot
ENTRYPOINT ["/app"]
