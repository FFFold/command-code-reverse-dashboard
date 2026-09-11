# syntax=docker/dockerfile:1

# ── build stage ───────────────────────────────────────
FROM golang:1.27-alpine AS build
WORKDIR /src

# Layer-cache the module files first.
COPY go.mod ./
RUN go mod download

COPY . .
ARG VERSION=dev
RUN CGO_ENABLED=0 GOOS=linux go build \
      -trimpath -ldflags="-s -w -X main.buildVersion=${VERSION}" \
      -o /out/credit-dashboard ./cmd/credit-dashboard

# ── runtime stage ─────────────────────────────────────
# The frontend is embedded via go:embed, so a static binary is all we need.
FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app

COPY --from=build /out/credit-dashboard /app/credit-dashboard

# Distroless nonroot already runs as uid 65532.
EXPOSE 8787
ENTRYPOINT ["/app/credit-dashboard"]
