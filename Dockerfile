# syntax=docker/dockerfile:1.7

# --- build stage ---
FROM golang:1.26-alpine AS build

WORKDIR /src
RUN apk add --no-cache git make

COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=dev
ARG COMMIT=none
ARG BUILD_DATE=unknown

RUN CGO_ENABLED=0 GOOS=linux go build \
      -ldflags "-s -w \
        -X main.Version=${VERSION} \
        -X main.Commit=${COMMIT} \
        -X main.BuildDate=${BUILD_DATE}" \
      -o /out/olymp ./cmd/olymp

# --- runtime stage ---
FROM gcr.io/distroless/static-debian12:nonroot AS runtime

WORKDIR /app
COPY --from=build /out/olymp /app/olymp

USER nonroot:nonroot
EXPOSE 8080

# distroless has no shell — health probes hit /healthz directly.
ENTRYPOINT ["/app/olymp"]
CMD ["serve", "--addr", ":8080"]
