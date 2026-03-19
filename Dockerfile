FROM golang:1.24-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /sync .

FROM alpine:3.21
RUN apk add --no-cache git github-cli
COPY --from=builder /sync /sync
ENTRYPOINT ["/sync"]
