# agentsfs Hub — Option A container: the afs-hub binary plus real git
# (git-http-backend). Repos live on a mounted volume (/data); object storage
# is used later for durability/LFS. Portable to any container host — Fly.io,
# a VPS, or Cloudflare Containers.

# ---- build ----
FROM golang:1.26-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -o /out/afs-hub ./cmd/afs-hub

# ---- runtime ----
FROM alpine:3.20
# git for the core commands; git-daemon supplies git-http-backend (the CGI the
# hub wraps) under /usr/libexec/git-core; ca-certs for outbound TLS.
RUN apk add --no-cache git git-daemon ca-certificates
COPY --from=build /out/afs-hub /usr/local/bin/afs-hub
# Repos persist here; mount a volume in production.
VOLUME /data
EXPOSE 8080
# Tokens are supplied at runtime via AFS_HUB_TOKENS (never baked into the image).
ENTRYPOINT ["/usr/local/bin/afs-hub", "--addr", ":8080", "--dir", "/data"]
