# Build stage
FROM golang:1.25-alpine AS builder

WORKDIR /app

# Copy dependency files and download
COPY go.mod go.sum ./
RUN go mod download

# Copy source files
COPY . .

# Build static binary
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w" -o /zef-workspace src/cmd/server/main.go

# Run stage
FROM alpine:3.19

RUN apk --no-cache add ca-certificates tzdata

WORKDIR /app

COPY --from=builder /zef-workspace .

# Expose port
EXPOSE 8085

# Run the app
CMD ["./zef-workspace"]
