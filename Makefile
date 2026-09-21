# Сборка и тесты — только в контейнере (хостовый go запрещён, см. CLAUDE.md).
# Кеши модулей/сборки живут в докер-томах, не на хосте.
GO_IMAGE  := golang:1.27-alpine
MOD_VOL   := lazy1c-gomod
CACHE_VOL := lazy1c-gocache
RUN       := docker run --rm -v "$(CURDIR)":/src -w /src -v $(MOD_VOL):/go/pkg/mod -v $(CACHE_VOL):/root/.cache/go-build $(GO_IMAGE)

.PHONY: test vet fmt tidy build build-linux build-windows clean

test: ## юнит-тесты (живые стенды — за RA82TEST, по умолчанию скипаются)
	$(RUN) go test ./...

vet:
	$(RUN) go vet ./...

fmt: ## проверка форматирования (gofmt -l должен быть пуст)
	$(RUN) sh -c 'out=$$(gofmt -l .); test -z "$$out" || { echo "$$out"; exit 1; }'

tidy:
	$(RUN) go mod tidy

build: ## бинарник под текущий мак (кросс-компиляция из linux-контейнера)
	docker run --rm -v "$(CURDIR)":/src -w /src -v $(MOD_VOL):/go/pkg/mod -v $(CACHE_VOL):/root/.cache/go-build \
		-e GOOS=darwin -e GOARCH=arm64 -e CGO_ENABLED=0 $(GO_IMAGE) \
		go build -trimpath -o lazy1c .

build-linux: ## бинарник под linux/amd64 (сервера)
	docker run --rm -v "$(CURDIR)":/src -w /src -v $(MOD_VOL):/go/pkg/mod -v $(CACHE_VOL):/root/.cache/go-build \
		-e GOOS=linux -e GOARCH=amd64 -e CGO_ENABLED=0 $(GO_IMAGE) \
		go build -trimpath -o lazy1c-linux .

build-windows: ## бинарник под windows/amd64
	docker run --rm -v "$(CURDIR)":/src -w /src -v $(MOD_VOL):/go/pkg/mod -v $(CACHE_VOL):/root/.cache/go-build \
		-e GOOS=windows -e GOARCH=amd64 -e CGO_ENABLED=0 $(GO_IMAGE) \
		go build -trimpath -o lazy1c.exe .

clean:
	rm -f lazy1c lazy1c-linux lazy1c.exe
