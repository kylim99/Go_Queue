FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /goqueue ./cmd/goqueue

FROM alpine:3.19
RUN apk add --no-cache ca-certificates
COPY --from=builder /goqueue /goqueue
EXPOSE 8080
CMD ["/goqueue"]
