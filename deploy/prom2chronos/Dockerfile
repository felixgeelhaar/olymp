FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /out/prom2chronos .

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/prom2chronos /app/prom2chronos
USER nonroot:nonroot
ENTRYPOINT ["/app/prom2chronos"]
