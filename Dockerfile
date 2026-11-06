FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /tributary ./cmd/server
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /tributary-cli ./cmd/cli

FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /tributary /tributary
COPY --from=build /tributary-cli /tributary-cli
EXPOSE 8080
ENTRYPOINT ["/tributary"]
