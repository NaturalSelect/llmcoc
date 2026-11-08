# ─── Frontend build stage ──────────────────────────────────────────────────────
# NOTE: vite.config.ts 的 outDir 是 ../cmd/server/dist/app（相对 frontend/），所以这里保持 /src/frontend 的目录结构，产物就落在 /src/cmd/server/dist/app
FROM node:22-alpine AS web

WORKDIR /src/frontend
COPY frontend/package.json frontend/package-lock.json ./
RUN npm ci

COPY frontend/ ./
RUN npm run build

# ─── Build stage ───────────────────────────────────────────────────────────────
FROM golang:1.26-alpine3.24 AS builder

RUN apk add --no-cache gcc musl-dev sqlite-dev

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
COPY --from=web /src/cmd/server/dist/app ./cmd/server/dist/app
RUN CGO_ENABLED=1 GOOS=linux go build -ldflags="-s -w" -o /app/llmcoc ./cmd/server

# ─── Runtime stage ─────────────────────────────────────────────────────────────
FROM alpine:3.24

RUN apk add --no-cache ca-certificates sqlite-libs tzdata
ENV TZ=Asia/Shanghai

WORKDIR /app
COPY --from=builder /app/llmcoc .
COPY config.yaml COC_kp.md COC_spell.md COC_monster.md ./

RUN mkdir -p data

EXPOSE 8080

CMD ["/app/llmcoc"]
