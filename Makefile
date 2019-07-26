BINARY := resize-thyself
VERSION := 0.0.1

BINARY: *.go
	go build .

dryrun: BINARY
	./resize-thyself --dryrun --threshold=0

.PHONY: test
test: lint
	go test $(PKGS)

.PHONY: lint
lint: $(GOMETALINTER)
	golangci-lint run 

.PHONY: release
release:
	mkdir -p release
	go build -o release/$(BINARY)-$(VERSION)-linux-amd64
	github-release "v$(VERSION)" release/$(BINARY)-$(VERSION)-linux-amd64 \
          --commit "master" \
          --tag "$(VERSION)" \
          --github-repository "solarkennedy/resize-thyself"

fmt:
	gofmt -w *.go
