# Nexus Agent —— 自进化 AI 代理运行时 (Go 版)
FROM golang:1.25-alpine AS builder

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/nexus ./cmd/nexus
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/nexus-gateway ./cmd/nexus-gateway
RUN CGO_ENABLED=0 go build -ldflags="-s -w" -o /out/nexus-acp ./cmd/nexus-acp

FROM alpine:3.21
RUN apk --no-cache add ca-certificates

WORKDIR /app
COPY --from=builder /out/nexus /out/nexus-gateway /out/nexus-acp /app/

EXPOSE 8080

ENTRYPOINT ["/app/nexus"]
CMD ["chat"]
