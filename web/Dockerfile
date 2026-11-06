# The built app is the same on every platform, so it's built once on the machine's own.
FROM --platform=$BUILDPLATFORM node:22-alpine AS build
WORKDIR /src
RUN npm install --global pnpm@11.1.3
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile
COPY . .
RUN pnpm build

# The web server uses only Node's standard library, so no node_modules ship.
FROM node:22-alpine
ARG VERSION=dev
LABEL org.opencontainers.image.title="Tributary web" \
      org.opencontainers.image.description="The web app of Tributary, a self-hosted file manager that joins Google Drive accounts into one pool." \
      org.opencontainers.image.url="https://tributary.swayam.li" \
      org.opencontainers.image.source="https://github.com/swayam5342/tributary" \
      org.opencontainers.image.licenses="MIT" \
      org.opencontainers.image.version="${VERSION}"
WORKDIR /app
COPY --from=build /src/dist dist
COPY server/app.mjs server/main.mjs server/
USER node
EXPOSE 3000
CMD ["node", "server/main.mjs"]
