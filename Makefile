.PHONY: build clean

build:
	go mod tidy
	go build -o server .

clean:
	rm -f server
