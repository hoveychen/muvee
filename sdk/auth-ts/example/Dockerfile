# muvee deployer runs `docker build -f sdk/auth-ts/example/Dockerfile <repo-root>`,
# so every COPY path here is rooted at the repo root, not at this Dockerfile's
# directory.

# Build stage: install deps and produce dist/ via vite.
FROM node:20-alpine AS build
WORKDIR /app
COPY sdk/auth-ts/example/package.json sdk/auth-ts/example/package-lock.json* ./
RUN npm install --no-audit --no-fund
COPY sdk/auth-ts/example/tsconfig.json \
     sdk/auth-ts/example/vite.config.ts \
     sdk/auth-ts/example/index.html \
     ./
COPY sdk/auth-ts/example/src ./src
RUN npm run build

# Runtime stage: nginx serves the static bundle on port 8080. SPA fallback so
# any deep link hits index.html and lets React handle routing.
FROM nginx:alpine
COPY --from=build /app/dist /usr/share/nginx/html
COPY sdk/auth-ts/example/nginx.conf /etc/nginx/conf.d/default.conf
EXPOSE 8080
