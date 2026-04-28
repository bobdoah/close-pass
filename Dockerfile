FROM golang:1.22-alpine AS builder
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o /out/close-pass ./cmd/close-pass

FROM alpine:3.20
RUN apk add --no-cache ffmpeg ca-certificates tzdata
COPY --from=builder /out/close-pass /usr/local/bin/close-pass

ENV DATA_DIR=/data
ENV REPORTS_ROOT=/reports
ENV HTTP_ADDR=:8080
EXPOSE 8080

VOLUME ["/data", "/reports"]
ENTRYPOINT ["/usr/local/bin/close-pass"]
