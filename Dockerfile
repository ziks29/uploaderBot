FROM golang:1.23-alpine AS builder
WORKDIR /build
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o uploaderBot .

FROM alpine:3.19
RUN apk --no-cache add ca-certificates tzdata
RUN addgroup -S bot && adduser -S bot -G bot
WORKDIR /app
COPY --from=builder /build/uploaderBot .
COPY lang/ lang/
RUN mkdir -p data && chown -R bot:bot /app
USER bot
ENV KEYS_FILE=/app/data/keys.json
ENTRYPOINT ["./uploaderBot"]
