# Makefile do slog
#
# Comandos principais:
#   make build     compila o binário em ./bin/slog
#   make run       compila e executa (use ARGS="..." para passar flags)
#   make install   instala o comando `slog` na máquina (em $(GOBIN))
#   make uninstall remove o comando `slog` instalado
#   make test      roda os testes
#   make clean     remove binários e bancos de logs gerados

# Caminho do pacote principal (comando slog).
PKG := ./cmd/slog
# Nome do binário gerado.
BINARY := slog
# Diretório de saída do build local.
BIN_DIR := bin

# Diretório onde `go install` coloca o binário. Respeita GOBIN; caso não esteja
# definido, usa $(GOPATH)/bin (padrão do Go).
GOBIN ?= $(shell go env GOBIN)
ifeq ($(GOBIN),)
GOBIN := $(shell go env GOPATH)/bin
endif

# Flags extras repassadas em `make run` (ex.: make run ARGS="-web :9000").
ARGS ?=

.DEFAULT_GOAL := build

.PHONY: build
build: ## Compila o binário em ./bin/slog
	@mkdir -p $(BIN_DIR)
	go build -o $(BIN_DIR)/$(BINARY) $(PKG)
	@echo "binário gerado em $(BIN_DIR)/$(BINARY)"

.PHONY: run
run: build ## Compila e executa (use ARGS="..." para passar flags)
	./$(BIN_DIR)/$(BINARY) $(ARGS)

.PHONY: install
install: ## Instala o comando `slog` na máquina (em $(GOBIN))
	go install $(PKG)
	@echo "slog instalado em $(GOBIN)/$(BINARY)"
	@echo "garanta que $(GOBIN) está no seu PATH para rodar 'slog' de qualquer lugar"

.PHONY: uninstall
uninstall: ## Remove o comando `slog` instalado
	rm -f $(GOBIN)/$(BINARY)
	@echo "slog removido de $(GOBIN)"

.PHONY: test
test: ## Roda os testes
	go test ./...

.PHONY: tidy
tidy: ## Sincroniza as dependências (go mod tidy)
	go mod tidy

.PHONY: clean
clean: ## Remove binários e bancos de logs gerados
	rm -rf $(BIN_DIR)
	rm -f *.db *.db-wal *.db-shm
	@echo "limpeza concluída"

.PHONY: db-clear
db-clear: build ## Remove os arquivos .db do diretório atual (use ARGS="-f" para não confirmar)
	./$(BIN_DIR)/$(BINARY) db:clear $(ARGS)

.PHONY: help
help: ## Lista os comandos disponíveis
	@grep -E '^[a-zA-Z_-]+:.*?## .*$$' $(MAKEFILE_LIST) | \
		awk 'BEGIN {FS = ":.*?## "}; {printf "  \033[36m%-12s\033[0m %s\n", $$1, $$2}'
