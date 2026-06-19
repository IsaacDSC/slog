# Exemplo de request

O servidor de exemplo (`go run ./example`) expõe `POST /log` na porta `3333`.
O corpo aceita os campos:

| Campo   | Tipo     | Descrição                                  |
|---------|----------|--------------------------------------------|
| `level` | string   | `INFO`, `WARN` ou `ERROR`                   |
| `msg`   | string   | Mensagem do log                            |
| `data`  | objeto   | Atributos extras (viram key/value no slog) |

```sh
curl -i -X POST http://localhost:3333/log \
  -H 'Content-Type: application/json' \
  -d '{
    "level": "INFO",
    "msg": "usuário autenticado",
    "data": {
      "user_id": 42,
      "ip": "10.0.0.1"
    }
  }'

```

Encadeando com o slog para persistir no SQLite:

```sh
go run ./example | go run ./cmd/slog -db logs.db
```
