# UI Next.js (standalone). Image akhir hanya berisi server.js + aset.
FROM node:20-alpine AS deps
WORKDIR /app
COPY package.json package-lock.json ./
RUN npm ci --no-audit --no-fund

FROM node:20-alpine AS bangun
WORKDIR /app
ENV NEXT_TELEMETRY_DISABLED=1
COPY --from=deps /app/node_modules ./node_modules
COPY . .
RUN npm run lint && npm run build

FROM node:20-alpine
WORKDIR /app
ENV NODE_ENV=production NEXT_TELEMETRY_DISABLED=1 PORT=3000 HOSTNAME=0.0.0.0
COPY --from=bangun /app/.next/standalone ./
COPY --from=bangun /app/.next/static ./.next/static
COPY --from=bangun /app/public ./public
USER node
EXPOSE 3000
CMD ["node", "server.js"]
