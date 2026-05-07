.PHONY: build test lint vet clean install

BINARY := prmigrate
CMD     := ./cmd/prmigrate

build:
	go build -o $(BINARY) $(CMD)

install:
	go install $(CMD)

test:
	go test ./...

lint:
	golangci-lint run ./...

vet:
	go vet ./...

clean:
	rm -f $(BINARY)
