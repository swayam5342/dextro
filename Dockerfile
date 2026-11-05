FROM node:22-alpine AS web
WORKDIR /src/web
RUN npm install --global pnpm@11.1.3
COPY web/package.json web/pnpm-lock.yaml web/pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile
COPY web/ ./
RUN pnpm build

FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd/ cmd/
COPY internal/ internal/
COPY web/embed.go web/embed.go
COPY --from=web /src/web/dist web/dist
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /dextro ./cmd/server

FROM gcr.io/distroless/static-debian13:nonroot
COPY --from=build /dextro /dextro
EXPOSE 8080
ENTRYPOINT ["/dextro"]
