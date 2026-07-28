FROM golang:1.25-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/mipmi ./cmd/mipmi

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /
COPY --from=build /out/mipmi /mipmi
USER nonroot:nonroot
EXPOSE 8080
ENV MIPMI_DATA_DIR=/data
VOLUME ["/data"]
ENTRYPOINT ["/mipmi"]
