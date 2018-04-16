.PHONY: dep build test docker release

dep:
	dep ensure --vendor-only

build:
	go build

test:
	go test ./...

docker:
	docker build -t gochain/chainload .

release: docker
	./release.sh
