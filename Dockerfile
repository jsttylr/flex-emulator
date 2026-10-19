# Flex Emulator as a server: settings page on the network, password from FLEXEMU_PASSWORD.
# Run it with host networking so the Flex discovery broadcast reaches your LAN.
FROM --platform=$BUILDPLATFORM golang:1.24-alpine AS build
ARG TARGETOS TARGETARCH
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags="-s -w" -o /flex-emulator .

FROM gcr.io/distroless/static-debian12
LABEL org.opencontainers.image.title="Flex Emulator" \
      org.opencontainers.image.description="Makes non-Flex radios look like a FlexRadio to Antenna Genius and other Flex accessories" \
      org.opencontainers.image.licenses="GPL-3.0-or-later" \
      org.opencontainers.image.source="https://github.com/jsttylr/flex-emulator"
COPY --from=build /flex-emulator /flex-emulator
ENV FLEXEMU_CONFIG=/config/config.json \
    FLEXEMU_LISTEN=0.0.0.0:8992 \
    FLEXEMU_NO_BROWSER=true
VOLUME /config
EXPOSE 4992/tcp 4992/udp 8992/tcp
HEALTHCHECK --interval=30s --timeout=5s --start-period=10s CMD ["/flex-emulator", "-healthcheck"]
ENTRYPOINT ["/flex-emulator"]
