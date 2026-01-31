FROM golang:1.24-alpine AS builder

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o groq-go .

FROM alpine:latest
RUN apk --no-cache add ca-certificates git nodejs npm python3 bash

WORKDIR /app
COPY --from=builder /app/groq-go .

EXPOSE 8080

CMD ["./groq-go", "-web", "-addr", ":8080"]
