build:
	go build -o mimicode ./cmd/mimicode

test:
	go test ./...

install:
	go install ./cmd/mimicode

clean:
	rm -f mimicode
