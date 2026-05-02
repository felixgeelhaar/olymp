# Multi-target image: build both broken sample services in one stage,
# select via the SERVICE build arg (default: flaky-payments).
FROM golang:1.24-alpine AS build
WORKDIR /src
COPY go.mod go.sum* ./
RUN go mod download
COPY . .
ARG SERVICE=flaky-payments
RUN CGO_ENABLED=0 go build -o /out/svc ./cmd/${SERVICE}

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/svc /app/svc
USER nonroot:nonroot
ENTRYPOINT ["/app/svc"]
