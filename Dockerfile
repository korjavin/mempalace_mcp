## Build Go binary
FROM golang:1.24-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o server ./cmd/server

## Runtime
FROM python:3.12-slim

RUN apt-get update && apt-get install -y --no-install-recommends ca-certificates && rm -rf /var/lib/apt/lists/*

# Install mempalace
RUN pip install --no-cache-dir mempalace

WORKDIR /app
COPY --from=builder /app/server .

# Set HOME so ~/.mempalace/ resolves inside the container
ENV HOME=/app

# Palace data volume
RUN mkdir -p /data/palace
VOLUME /data/palace

ENV PORT=8080
ENV PALACE_PATH=/data/palace

EXPOSE 8080

CMD ["./server"]
