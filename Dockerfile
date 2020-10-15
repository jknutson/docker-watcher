FROM golang:1.15
COPY . /go/project
WORKDIR /go/project
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o ./docker-watcher -v
CMD ["./docker-watcher"]
