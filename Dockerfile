# ---- build stage ----
FROM golang:1.26-alpine AS build
WORKDIR /src

# Cache dependencies first.
COPY go.mod go.sum ./
RUN go mod download

# Build a static binary (CGO off) so it runs on a scratch/distroless base.
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" \
    -o /out/linkforge ./cmd/server

# ---- runtime stage ----
FROM alpine:3.20
RUN apk add --no-cache ca-certificates && adduser -D -u 10001 app
USER app
COPY --from=build /out/linkforge /usr/local/bin/linkforge
EXPOSE 8080
ENTRYPOINT ["linkforge"]
