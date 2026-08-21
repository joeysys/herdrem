.PHONY: build web dev run clean

build: web
	go build -o herdrem ./cmd/herdrem

web:
	cd web && npm install && npm run build

dev:
	./scripts/dev.sh

run: build
	./herdrem serve --web

clean:
	rm -f herdrem
	rm -rf webdist/*
