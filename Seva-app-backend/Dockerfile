FROM golang:1.24 AS builder

WORKDIR /app

COPY go.mod go.sum ./
RUN go mod tidy

COPY . .

RUN go build -o seva-app-backend .

FROM alpine:latest


WORKDIR /root/

COPY --from=builder /app/seva-app-backend .

EXPOSE 8080

CMD ["./seva-app-backend"]
