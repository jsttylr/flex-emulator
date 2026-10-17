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
COPY --from=build /flex-emulator /flex-emulator
ENV FLEXEMU_CONFIG=/config/config.json \
    FLEXEMU_LISTEN=0.0.0.0:8992 \
    FLEXEMU_NO_BROWSER=true
VOLUME /config
ENTRYPOINT ["/flex-emulator"]
