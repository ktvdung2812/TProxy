.PHONY: build dashboard test vet verify run clean

dashboard:
	npm --prefix web install
	npm --prefix web run build

build: dashboard
	go build -o bin/tproxy ./cmd/tproxy

test:
	go test ./...

vet:
	go vet ./...

verify: dashboard test vet
	go test -race ./...
	go build -trimpath -o /tmp/tproxy-verify ./cmd/tproxy

run:
	@test -f .env.run && . ./.env.run; \
	go run ./cmd/tproxy --config config.yaml

clean:
	go clean
