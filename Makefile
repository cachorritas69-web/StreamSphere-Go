.PHONY: setup build test run stop health smoke clean frontend

setup:
	cp -n .env.example .env || true
	go mod download

build:
	mkdir -p bin
	go build -o bin/auth ./services/auth
	go build -o bin/catalog ./services/catalog
	go build -o bin/media ./services/media
	go build -o bin/playback ./services/playback
	go build -o bin/social ./services/social
	go build -o bin/analytics ./services/analytics
	go build -o bin/notification ./services/notification
	go build -o bin/gateway ./services/gateway
	go build -o bin/frontend ./services/frontend

test:
	go test ./...
	go vet ./...

run:
	bash scripts/start-local.sh

stop:
	bash scripts/stop-local.sh

health:
	bash scripts/health-local.sh

smoke:
	bash scripts/smoke-test.sh

frontend:
	bash scripts/build-frontend.sh

clean:
	bash scripts/stop-local.sh || true
	rm -rf .run bin data media frontend/dist
