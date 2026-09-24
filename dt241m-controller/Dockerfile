FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY main.go ./
COPY internal ./internal
ARG BUILD_VERSION=dev
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w -X main.version=${BUILD_VERSION}" -o /out/dt241m-controller .

FROM alpine:3.24

ARG BUILD_VERSION=dev
ARG BUILD_ARCH

RUN apk add --no-cache ca-certificates
COPY --from=build /out/dt241m-controller /usr/local/bin/dt241m-controller

LABEL \
  io.hass.name="DT241M Controller" \
  io.hass.description="MQTT bridge for PWAY DT241M HDMI-over-IP devices" \
  io.hass.version="${BUILD_VERSION}" \
  io.hass.type="app" \
  io.hass.arch="aarch64|amd64"

CMD ["/usr/local/bin/dt241m-controller"]
