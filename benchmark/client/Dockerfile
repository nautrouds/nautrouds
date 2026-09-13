FROM golang:1.25-alpine
WORKDIR /app
COPY go.mod ./
COPY bench_test.go ./
CMD ["go", "test", "-bench=.", "-benchtime=5s", "-run=^$", "./..."]
