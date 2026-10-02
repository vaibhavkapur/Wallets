FROM golang:1.27-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/ ./cmd/...

FROM gcr.io/distroless/static-debian12:nonroot AS controlplane
COPY --from=build /out/controlplane /controlplane
USER nonroot
ENTRYPOINT ["/controlplane"]

FROM gcr.io/distroless/static-debian12:nonroot AS worker
COPY --from=build /out/worker /worker
USER nonroot
ENTRYPOINT ["/worker"]

FROM gcr.io/distroless/static-debian12:nonroot AS mocksigner
COPY --from=build /out/mocksigner /mocksigner
USER nonroot
ENTRYPOINT ["/mocksigner"]

FROM gcr.io/distroless/static-debian12:nonroot AS mockresource
COPY --from=build /out/mockresource /mockresource
USER nonroot
ENTRYPOINT ["/mockresource"]
