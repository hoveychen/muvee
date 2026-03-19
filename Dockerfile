# Dockerfile — downloads the pre-built muvee binary from GitHub Releases.
# Requires internet access during build. For air-gapped / offline builds use Dockerfile.local.
#
# Build args:
#   VERSION   — release tag to download, e.g. v0.1.0  (default: latest)
#   TARGETOS  — linux | darwin                         (default: linux)
#   TARGETARCH — amd64 | arm64                         (default: amd64)

ARG VERSION=latest
ARG TARGETOS=linux
ARG TARGETARCH=amd64

FROM alpine:3.21 AS downloader
ARG VERSION
ARG TARGETOS
ARG TARGETARCH

RUN apk add --no-cache ca-certificates curl

RUN set -eux; \
    if [ "$VERSION" = "latest" ]; then \
      URL="https://github.com/hoveychen/muvee/releases/latest/download/muvee_${TARGETOS}_${TARGETARCH}"; \
    else \
      URL="https://github.com/hoveychen/muvee/releases/download/${VERSION}/muvee_${TARGETOS}_${TARGETARCH}"; \
    fi; \
    echo "Downloading $URL"; \
    curl -fsSL "$URL" -o /muvee; \
    chmod +x /muvee

# ── Runtime image ──────────────────────────────────────────────────────────────
# agent role needs rsync + git + docker CLI; server/authservice only need ca-certs.
# Install all tools so one image covers all roles.
FROM alpine:3.21
RUN apk add --no-cache ca-certificates rsync git docker-cli docker-cli-buildx

COPY --from=downloader /muvee /usr/local/bin/muvee

EXPOSE 8080 4181

ENTRYPOINT ["muvee"]
# Override CMD at runtime:
#   docker run ... muvee server
#   docker run ... muvee agent
#   docker run ... muvee authservice
