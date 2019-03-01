.PHONY: build test docker release

version=$(shell ./version.sh)

build:
	go build -ldflags "-X main.version=${version}" ./cmd/chainload

test:
	go test ./...

docker:
	docker build --build-arg VERSION=${version} -t gochain/chainload .

release: docker
	./release.sh
