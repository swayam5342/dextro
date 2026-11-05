FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /dextro ./cmd/server

FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /dextro /dextro
EXPOSE 8080
ENTRYPOINT ["/dextro"]
