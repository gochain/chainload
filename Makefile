.PHONY: dep docker

dep:
	dep ensure --vendor-only

docker:
	docker build -t gochain/chainload .
