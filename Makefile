.PHONY: build dashboard test vet verify run clean

dashboard:
	npm --prefix web ci
	npm --prefix web run build

build: dashboard
	mkdir -p bin
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

dev:
	npm run dev

clean:
	go clean
