# ─── Build stage ───────────────────────────────────────────────────────────────
FROM golang:1.26-alpine3.24 AS builder

RUN apk add --no-cache gcc musl-dev sqlite-dev

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
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
