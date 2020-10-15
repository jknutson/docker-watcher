# build:
# 	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -ldflags "-X main.buildVersion=novu" -o health_check ./health_check.go

# Makefile template borrowed from https://sohlich.github.io/post/go_makefile/
PROJECT=docker-watcher
PROJECT_VERSION=`cat VERSION.txt`
# Go parameters
GOCMD=go
GOBUILD=$(GOCMD) build
GOCLEAN=$(GOCMD) clean
GOTEST=$(GOCMD) test
GOGET=$(GOCMD) get
GOVERSION=1.15
GOFLAGS="-X main.buildVersion=$(PROJECT_VERSION)"
BINARY_NAME=$(PROJECT)

all: test build
build:
	$(GOBUILD) -ldflags $(GOFLAGS) -o "$(BINARY_NAME)" -v
test:
	$(GOTEST) -v ./...
clean:
	$(GOCLEAN)
	rm -f $(BINARY_NAME)
run:
	$(GOBUILD) -o $(BINARY_NAME) -v ./...
	./$(BINARY_NAME)
deps:
	GO111MODULE=on $(GOGET) github.com/docker/docker/client@master

# Cross compilation
build-linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 $(GOBUILD) -o "$(BINARY_NAME)" -v
docker-build:
	docker run --rm -it -v "$(GOPATH)":/go -w "/go/src/github.com/novu/$(PROJECT)" golang:$(GOVERSION) $(GOBUILD) -o "$(BINARY_NAME)" -v
