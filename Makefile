.PHONY: dep build test docker

dep:
	dep ensure --vendor-only

build:
	go build

test:
	go test ./...

docker:
	docker build -t gochain/chainload .
