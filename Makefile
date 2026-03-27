.PHONY: build test test-race run lint docker-build

BINARY := hub-router
CMD := ./cmd/hub-router

build:
	go build -o $(BINARY) $(CMD)

test:
	go test ./...

test-race:
	go test -race ./...

run:
	HR_ONLINE_API_KEYS=online-secret \
	HR_LOCAL_API_KEYS=local-secret \
	go run $(CMD)

lint:
	golangci-lint run

tidy:
	go mod tidy

docker-build:
	docker build -t hub-router:latest .

docker-up:
	docker-compose -f deploy/docker-compose.yml up -d

docker-down:
	docker-compose -f deploy/docker-compose.yml down

clean:
	rm -f $(BINARY)
