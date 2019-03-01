.PHONY: dep build test docker release

build:
	go build

test:
	go test ./...

docker:
	docker build -t gochain/chainload .

release: docker
	./release.sh
