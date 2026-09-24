FROM node:24-alpine AS build
WORKDIR /app
RUN npm install -g pnpm@11
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile
COPY tsconfig.json tsconfig.build.json ./
COPY src ./src
RUN pnpm build

FROM node:24-alpine AS prod-deps
WORKDIR /app
RUN npm install -g pnpm@11
COPY package.json pnpm-lock.yaml pnpm-workspace.yaml ./
RUN pnpm install --prod --frozen-lockfile \
 && rm -rf node_modules/.pnpm/better-sqlite3@*/node_modules/better-sqlite3/deps \
           node_modules/.pnpm/better-sqlite3@*/node_modules/better-sqlite3/src

FROM ghcr.io/home-assistant/base:3.24-2026.08.0

ARG BUILD_VERSION=1.0.1
ARG BUILD_ARCH

RUN apk add --no-cache nodejs

WORKDIR /app
COPY --from=prod-deps /app/node_modules ./node_modules
COPY --from=build /app/dist ./dist
COPY package.json ./
COPY drizzle ./drizzle
COPY run.sh /run.sh
RUN chmod a+x /run.sh \
 && node -e "const v=process.versions; const D=require('better-sqlite3'); console.log('node', v.node, 'abi', v.modules, 'sqlite', new D(':memory:').prepare('select sqlite_version() v').get().v)"

LABEL \
  io.hass.name="DT241M Controller" \
  io.hass.description="MQTT bridge for PWAY DT241M HDMI-over-IP devices" \
  io.hass.version="${BUILD_VERSION}" \
  io.hass.type="app" \
  io.hass.arch="aarch64|amd64"

CMD ["/run.sh"]
