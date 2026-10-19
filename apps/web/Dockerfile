# syntax=docker/dockerfile:1
FROM node:22-alpine AS build
WORKDIR /app
ENV COREPACK_ENABLE_DOWNLOAD_PROMPT=0
RUN corepack enable
COPY package.json pnpm-lock.yaml ./
RUN --mount=type=cache,target=/root/.cache/pnpm pnpm install --frozen-lockfile
COPY . .
RUN pnpm run build

# Unprivileged nginx: runs as uid 101 with its pid and temp files under /tmp, so it listens on 8080.
FROM nginxinc/nginx-unprivileged:1.27.5-alpine
COPY nginx.conf /etc/nginx/conf.d/default.conf
COPY --from=build /app/dist /usr/share/nginx/html
USER nginx
EXPOSE 8080
