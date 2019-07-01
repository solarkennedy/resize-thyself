BINARY := resize-theyself

.PHONY: test
test: lint
	go test $(PKGS)

.PHONY: lint
lint: $(GOMETALINTER)
	golangci-lint run 

release:
	mkdir -p release
	go build -o release/$(BINARY)-$(VERSION)-linux-amd64

fmt:
	gofmt -w *.go
