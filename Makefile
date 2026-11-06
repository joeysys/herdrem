.PHONY: build web dev run clean check-install bump

build: web
	go build -o herdrem ./cmd/herdrem

# Single source of truth is version/VERSION (embedded into the binary).
# npm version updates package.json and package-lock.json atomically.
bump:
	@test -n "$(V)" || (echo "usage: make bump V=x.y.z"; exit 1)
	cd web && npm version --no-git-tag-version "$(V)"
	@echo "$(V)" > version/VERSION

# HERDREM_PWA_NAME names the installed PWA (default Herdrem). Build time only.
web:
	cd web && npm install && npm run build

dev:
	./scripts/dev.sh

run: build
	./herdrem serve --web

clean:
	rm -f herdrem
	rm -rf webdist/*

check-install:
	./deploy/check-sync.sh
