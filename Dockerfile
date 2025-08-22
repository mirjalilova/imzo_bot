# build stage
FROM golang:1.21-alpine AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN go build -o bot main.go

FROM alpine:latest

WORKDIR /root/
COPY --from=builder /app/bot .

COPY .env .env

CMD ["./bot"]
