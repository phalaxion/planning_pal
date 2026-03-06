build:
	go build -o bin/server ./cmd

run:
	go run ./cmd

fmt:
	gofmt -w .
