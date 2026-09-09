# Build the web frontend only.
FROM node:22-alpine AS web-builder
WORKDIR /web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build  # outputs to /webdist

# Static Web/PWA image. The relay is installed and run on the host.
FROM nginx:1.27-alpine
COPY --from=web-builder /webdist/ /usr/share/nginx/html/
COPY deploy/web-nginx.conf /etc/nginx/conf.d/default.conf
EXPOSE 80
