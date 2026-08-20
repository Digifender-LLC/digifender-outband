# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1.25-bookworm AS build
ARG TARGETOS=linux
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -o /out/outband ./cmd/outband

# Seed /data (and media library + URL cache) as nonroot (65532) so first-time
# named volumes inherit writable ownership. Distroless has no shell.
RUN mkdir -p /out/data/media/.cache && chown -R 65532:65532 /out/data

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /
COPY --from=build /out/outband /outband
COPY --from=build --chown=65532:65532 /out/data /data
USER nonroot:nonroot
EXPOSE 8080
ENV OUTBAND_DATA_DIR=/data
ENV OUTBAND_MEDIA_DIR=/data/media
VOLUME ["/data"]
ENTRYPOINT ["/outband"]
