# The build runs on the machine's own platform and cross-compiles, which is much faster
# than emulating another one.
FROM --platform=$BUILDPLATFORM golang:1.27-alpine AS build
ARG TARGETOS TARGETARCH VERSION=dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
ENV CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH
RUN go build -trimpath -ldflags="-s -w -X github.com/swayam5342/tributary/internal/version.Version=${VERSION}" -o /tributary ./cmd/server
RUN go build -trimpath -ldflags="-s -w -X github.com/swayam5342/tributary/internal/version.Version=${VERSION}" -o /tributary-cli ./cmd/cli

FROM gcr.io/distroless/static-debian13:nonroot
ARG VERSION=dev
LABEL org.opencontainers.image.title="Tributary API" \
      org.opencontainers.image.description="The API server of Tributary, a self-hosted file manager that joins Google Drive accounts into one pool." \
      org.opencontainers.image.url="https://tributary.swayam.li" \
      org.opencontainers.image.source="https://github.com/swayam5342/tributary" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.version="${VERSION}"
COPY --from=build /tributary /tributary
COPY --from=build /tributary-cli /tributary-cli
EXPOSE 8080
ENTRYPOINT ["/tributary"]
