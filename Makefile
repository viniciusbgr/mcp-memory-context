BINARY := bin/mcp-memory-context

.PHONY: build clean

build:
	mkdir -p $(dir $(BINARY))
	go build -o $(BINARY) ./cmd/server

clean:
	rm -f $(BINARY)
