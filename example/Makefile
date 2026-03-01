.PHONY: generate build

generate:
	@command -v gqlgen >/dev/null 2>&1 || go install github.com/99designs/gqlgen@latest
	gqlgen generate

build: generate
	go build -o myapp

clean:
	rm -f myapp store.json
