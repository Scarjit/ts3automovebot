FROM golang:alpine AS builder
RUN apk update && apk add --no-cache git
WORKDIR /

# Copy go.mod and go.sum files
COPY go.mod go.sum ./
# Copy main.go
COPY main.go ./

# Download all dependencies. Dependencies will be cached if the go.mod and go.sum files are not changed
RUN go mod download

# Build the Go app
RUN GOOS=linux go build -a -installsuffix cgo -o main .

FROM alpine:latest AS runner
COPY --from=builder /main /app/main
ENTRYPOINT ["/app/main"]
