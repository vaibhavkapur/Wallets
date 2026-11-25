FROM golang:1.27-alpine AS backend-build
WORKDIR /src
COPY backend/go.mod backend/go.sum ./backend/
RUN cd backend && go mod download
COPY backend ./backend
RUN cd backend && CGO_ENABLED=0 go build -o /out/recurring-server ./cmd/server
RUN cd backend && CGO_ENABLED=0 go build -o /out/recurring-worker ./cmd/worker

FROM alpine:3.21
WORKDIR /app
RUN apk add --no-cache ca-certificates tzdata
COPY --from=backend-build /out/recurring-server /usr/local/bin/recurring-server
COPY --from=backend-build /out/recurring-worker /usr/local/bin/recurring-worker
EXPOSE 8090
CMD ["recurring-server"]
