FROM node:20.20.2-bookworm-slim AS web
WORKDIR /src/web
COPY web/package*.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.27.1-bookworm AS backend
WORKDIR /src/backend
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
RUN CGO_ENABLED=0 go build -trimpath -o /server ./cmd/server

FROM gcr.io/distroless/static-debian12:nonroot
COPY --from=backend /server /app/server
COPY --from=web /src/web/dist /app/web
WORKDIR /app
EXPOSE 8088
ENTRYPOINT ["/app/server"]
