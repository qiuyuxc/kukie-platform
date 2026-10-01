FROM --platform=$BUILDPLATFORM node:24-bookworm-slim AS console
WORKDIR /build/apps/console
COPY apps/console/package.json apps/console/package-lock.json ./
RUN npm ci
COPY apps/console/ ./
RUN npm run build

FROM --platform=$BUILDPLATFORM node:24-bookworm-slim AS site
ARG BUILDARCH
ARG KUKIE_SITE_URL=https://www.kukie.cn
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /tmp/hugo
RUN curl -fsSLO https://github.com/gohugoio/hugo/releases/download/v0.167.0/hugo_0.167.0_checksums.txt \
    && echo '3e660936247093840181ea5e0af5be3772e2f0c7720d6ce9e9645a12ab7531ec  hugo_0.167.0_checksums.txt' | sha256sum -c - \
    && curl -fsSLO "https://github.com/gohugoio/hugo/releases/download/v0.167.0/hugo_extended_0.167.0_linux-${BUILDARCH}.tar.gz" \
    && grep " hugo_extended_0.167.0_linux-${BUILDARCH}.tar.gz$" hugo_0.167.0_checksums.txt | sha256sum -c - \
    && tar -xzf "hugo_extended_0.167.0_linux-${BUILDARCH}.tar.gz" -C /usr/local/bin hugo
WORKDIR /build
COPY hugo.toml hugo.server.toml ./
COPY archetypes/ archetypes/
COPY assets/ assets/
COPY content/ content/
COPY data/ data/
COPY layouts/ layouts/
COPY static/ static/
COPY themes/ themes/
COPY apps/site/ apps/site/
COPY scripts/site.mjs scripts/site.mjs
RUN KUKIE_SITE_URL="$KUKIE_SITE_URL" KUKIE_SITE_OUTPUT=/site node scripts/site.mjs

FROM --platform=$BUILDPLATFORM golang:1.26-bookworm AS api
ARG TARGETARCH
RUN apt-get update && apt-get install -y --no-install-recommends gcc-aarch64-linux-gnu gcc-x86-64-linux-gnu \
    && rm -rf /var/lib/apt/lists/*
WORKDIR /build
COPY services/api/go.mod services/api/go.sum ./
RUN go mod download
COPY services/api/ ./
RUN case "$TARGETARCH" in amd64) compiler=x86_64-linux-gnu-gcc ;; arm64) compiler=aarch64-linux-gnu-gcc ;; *) exit 1 ;; esac \
    && CGO_ENABLED=1 GOOS=linux GOARCH="$TARGETARCH" CC="$compiler" go build -trimpath -ldflags='-s -w' -o /kukie-api .

FROM debian:bookworm-slim AS runtime
ARG KUKIE_SITE_URL=https://www.kukie.cn
RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates curl tzdata \
    && rm -rf /var/lib/apt/lists/* \
    && groupadd --gid 10001 kukie && useradd --uid 10001 --gid kukie --no-create-home kukie \
    && mkdir /data && chown kukie:kukie /data
WORKDIR /app
COPY --from=api /kukie-api /usr/local/bin/kukie-api
COPY --from=console /build/apps/console/dist/ /app/apps/console/dist/
COPY --from=site /site/ /app/site/
COPY content/posts/ /app/content/posts/
COPY static/ /app/static/
COPY LICENSE /app/LICENSE
ENV KUKIE_REPOSITORY=/app KUKIE_DATA_DIR=/data KUKIE_SITE_DIR=/app/site \
    KUKIE_SITE_ADDR=0.0.0.0:8086 KUKIE_API_ADDR=0.0.0.0:8084 KUKIE_CONSOLE_ADDR=0.0.0.0:8085 \
    KUKIE_SECURE_COOKIE=1 KUKIE_SITE_URL=$KUKIE_SITE_URL
USER kukie
EXPOSE 8084 8085 8086
HEALTHCHECK --interval=30s --timeout=10s --start-period=60s --retries=3 \
    CMD curl -fsS http://127.0.0.1:8084/api/v1/health >/dev/null && curl -fsS http://127.0.0.1:8085/ >/dev/null && curl -fsS http://127.0.0.1:8086/ >/dev/null || exit 1
ENTRYPOINT ["/usr/local/bin/kukie-api"]
