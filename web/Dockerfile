FROM node:22-alpine AS build
WORKDIR /src
RUN npm install --global pnpm@11.1.3
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile
COPY . .
RUN pnpm build

# The web server uses only Node's standard library, so no node_modules ship.
FROM node:22-alpine
WORKDIR /app
COPY --from=build /src/dist dist
COPY server/app.mjs server/main.mjs server/
USER node
EXPOSE 3000
CMD ["node", "server/main.mjs"]
