FROM golang:1.25-alpine AS builder
WORKDIR /app
COPY go.mod ./
COPY *.go ./
RUN CGO_ENABLED=0 go build -trimpath -o /bin/loadgen .

FROM alpine:3.21
COPY --from=builder /bin/loadgen /usr/local/bin/loadgen
CMD ["sleep", "infinity"]
