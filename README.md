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
| `-web`   | `:8080`    | Endereço da interface web (use `-web ""` para desligar) |

Encerra de forma limpa em `Ctrl+C` / `SIGTERM`, gravando o lote pendente.

## Interface web

Junto com a ingestão, o `slog` sobe uma interface web (por padrão em
`http://localhost:8080`) para visualizar e consultar os logs em tempo real:

```sh
meu-app | ./slog -db logs.db          # UI em http://localhost:8080
meu-app | ./slog -db logs.db -web :9000   # outra porta
meu-app | ./slog -db logs.db -web ""      # sem interface web
```

Recursos:

- **Logs ao vivo** — a lista atualiza sozinha via *polling* incremental.
- **Filtros** — busca por texto (em `msg`/`raw`), por nível e limite de linhas.
- **Console SQL** — consultas arbitrárias de **somente leitura**
  (`SELECT` / `WITH` / `PRAGMA` / `EXPLAIN`); comandos de escrita são recusados.
- **Tema claro/escuro**, com a preferência salva no navegador.

A interface permanece no ar **após o EOF** do `stdin` (quando o processo de
origem termina), para você continuar inspecionando os logs — encerre com
`Ctrl+C`. Os assets (incluindo o Handlebars) são embutidos no binário, então
funciona offline, sem CDN.

> A UI dá acesso de leitura ao banco a quem alcançar a porta. Como é uma
> ferramenta de uso local, não há autenticação; evite expô-la na rede.

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
