# muvee's builder runs `docker buildx build -f <abs-Dockerfile> <abs-Dockerfile-dir>`
# (see internal/builder/builder.go: build context = filepath.Dir(dockerfilePath)),
# so every COPY here is relative to this directory.

# Build stage: install deps and produce dist/ via vite.
FROM node:20-alpine AS build
WORKDIR /app
COPY package.json package-lock.json* ./
RUN npm install --no-audit --no-fund
COPY tsconfig.json vite.config.ts index.html ./
COPY src ./src
RUN npm run build

# Runtime stage: nginx serves the static bundle on port 8080. SPA fallback so
# any deep link hits index.html and lets React handle routing.
FROM nginx:alpine
COPY --from=build /app/dist /usr/share/nginx/html
COPY nginx.conf /etc/nginx/conf.d/default.conf
EXPOSE 8080
