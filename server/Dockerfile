FROM golang:1.22-alpine AS builder
RUN apk add --no-cache gcc musl-dev
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=1 go build -o /bin/gdoc-radar-server .

FROM alpine:3.20
RUN apk add --no-cache ca-certificates
COPY --from=builder /bin/gdoc-radar-server /usr/local/bin/
COPY templates/ /app/templates/
WORKDIR /app
EXPOSE 9902
CMD ["gdoc-radar-server", "--data-dir", "/data", "--port", "9902", "--host", "0.0.0.0"]
