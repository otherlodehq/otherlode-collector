FROM --platform=$BUILDPLATFORM golang:1.26.11-alpine@sha256:ae52b5bc6ed045a9ed26ab03dc19327a802a9cc9aa8b27ed57f5c6b60579b0bb AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
# Declared here, not at the top, so the module download above is cached once for every platform.
ARG TARGETOS
ARG TARGETARCH
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH go build -trimpath -ldflags='-s -w' -o /out/otherlode-collector ./cmd/otherlode-collector

FROM gcr.io/distroless/static-debian12:nonroot@sha256:afa5c872c891853ca7fcf1f12c3edb23f7eeef36189728842dd51042ff57f7ab
COPY --from=build /out/otherlode-collector /otherlode-collector
USER nonroot:nonroot
EXPOSE 4319
HEALTHCHECK --interval=10s --timeout=3s --start-period=10s --retries=3 CMD ["/otherlode-collector", "healthcheck"]
ENTRYPOINT ["/otherlode-collector"]
