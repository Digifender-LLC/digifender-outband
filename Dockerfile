# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1.25-bookworm AS build
ARG TARGETOS=linux
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -o /out/outband ./cmd/outband

# Seed /data as nonroot (65532) so first-time named volumes inherit writable ownership.
# Distroless has no shell; create the dir in the build stage and copy it across.
RUN mkdir -p /out/data && chown 65532:65532 /out/data

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /
COPY --from=build /out/outband /outband
COPY --from=build --chown=65532:65532 /out/data /data
USER nonroot:nonroot
EXPOSE 8080
ENV OUTBAND_DATA_DIR=/data
VOLUME ["/data"]
ENTRYPOINT ["/outband"]
