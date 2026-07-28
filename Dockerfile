# syntax=docker/dockerfile:1
FROM --platform=$BUILDPLATFORM golang:1.25-bookworm AS build
ARG TARGETOS=linux
ARG TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} go build -o /out/outband ./cmd/outband

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /
COPY --from=build /out/outband /outband
USER nonroot:nonroot
EXPOSE 8080
ENV OUTBAND_DATA_DIR=/data
VOLUME ["/data"]
ENTRYPOINT ["/outband"]
