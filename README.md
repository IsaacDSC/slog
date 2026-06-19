# slog

Lê logs em JSON do **stdin** e os persiste em um banco **SQLite**. Pensado para
capturar a saída estruturada de um processo via pipe Unix.

## Como usar

```sh
go build -o slog ./cmd/slog

# captura os logs de um app que escreve JSON no stdout
meu-app | ./slog -db logs.db

# também repassa as linhas adiante no pipe
meu-app | ./slog -echo | grep ERROR
```

### Flags

| Flag     | Padrão     | Descrição                                          |
|----------|------------|----------------------------------------------------|
| `-db`    | `logs.db`  | Caminho do arquivo SQLite                          |
| `-batch` | `100`      | Registros por gravação em lote                     |
| `-flush` | `1s`       | Tempo máximo antes de gravar um lote parcial       |
| `-echo`  | `false`    | Repassa cada linha para o stdout (passthrough)     |

Encerra de forma limpa em `Ctrl+C` / `SIGTERM`, gravando o lote pendente.

## Schema

Cada linha vira uma linha na tabela `logs`. Os campos `time`, `level` e `msg`
(convenção do `log/slog`) viram colunas próprias; os demais campos do JSON são
guardados como JSON em `attrs`. A linha original fica em `raw` — linhas que não
forem JSON válido são preservadas apenas em `raw`.

```sql
CREATE TABLE logs (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    time        TEXT,
    level       TEXT,
    msg         TEXT,
    attrs       TEXT,   -- demais campos, como JSON
    raw         TEXT NOT NULL,
    ingested_at TEXT NOT NULL
);
```

### Consultando

```sh
sqlite3 logs.db "SELECT time, level, msg FROM logs WHERE level = 'ERROR';"

sqlite3 logs.db "SELECT * from logs"
```
