SHELL := /bin/bash
.PHONY: build test bench vet models-check verify up down logs clean

build:
	go build ./...

vet:
	go vet ./...

test:
	go test ./... -count=1

bench:
	go test -bench . -benchmem ./kernel/...

models-check:
	python3 verify/check_real_data.py

verify: models-check
	bash verify/e2e.sh

up:
	test -f deploy/.env || cp deploy/.env.example deploy/.env
	docker compose -f deploy/docker-compose.yml up -d --build

down:
	docker compose -f deploy/docker-compose.yml down

logs:
	docker compose -f deploy/docker-compose.yml logs -f app

clean:
	rm -f llm-hub
	docker compose -f deploy/docker-compose.yml down --volumes --remove-orphans
