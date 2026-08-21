# Build the web frontend
FROM node:22-alpine AS web-builder
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build  # outputs to /webdist (vite outDir: ../webdist)

# Build the Go binary
FROM golang:1.26-alpine AS go-builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web-builder /webdist/ ./webdist/
RUN CGO_ENABLED=0 go build -o /herdrem ./cmd/herdrem

# Runtime image
FROM alpine:3.20
RUN apk add --no-cache ca-certificates cloudflared && \
    adduser -D -h /var/lib/herdrem herdrem
COPY --from=go-builder /herdrem /usr/local/bin/herdrem
USER herdrem
WORKDIR /var/lib/herdrem
VOLUME /var/lib/herdrem
EXPOSE 8766
ENTRYPOINT ["herdrem"]
CMD ["serve", "--web"]
