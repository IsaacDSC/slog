// Package store persiste registros de log em um banco SQLite.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// Entry representa um registro de log já normalizado, pronto para persistir.
type Entry struct {
	Time  string // timestamp vindo do próprio log (campo "time"), se houver
	Level string // nível do log ("INFO", "ERROR", ...), se houver
	Msg   string // mensagem principal ("msg"), se houver
	Attrs string // demais campos do JSON, serializados de volta como JSON
	Raw   string // linha original, exatamente como recebida
}

// Store encapsula a conexão com o SQLite e a gravação em lote.
type Store struct {
	db   *sql.DB
	stmt *sql.Stmt
}

const schema = `
CREATE TABLE IF NOT EXISTS logs (
	id          INTEGER PRIMARY KEY AUTOINCREMENT,
	time        TEXT,
	level       TEXT,
	msg         TEXT,
	attrs       TEXT,
	raw         TEXT NOT NULL,
	ingested_at TEXT NOT NULL,
	created_at  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_logs_time  ON logs(time);
CREATE INDEX IF NOT EXISTS idx_logs_level ON logs(level);
`

// Open abre (ou cria) o banco no caminho informado e garante o schema.
func Open(path string) (*Store, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("abrindo banco: %w", err)
	}

	// WAL melhora a concorrência leitura/escrita; busy_timeout evita "database is locked".
	pragmas := []string{
		"PRAGMA journal_mode = WAL;",
		"PRAGMA synchronous = NORMAL;",
		"PRAGMA busy_timeout = 5000;",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			db.Close()
			return nil, fmt.Errorf("aplicando pragma %q: %w", p, err)
		}
	}

	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("criando schema: %w", err)
	}

	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrando schema: %w", err)
	}

	stmt, err := db.Prepare(
		`INSERT INTO logs (time, level, msg, attrs, raw, ingested_at, created_at) VALUES (?, ?, ?, ?, ?, ?, ?)`,
	)
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("preparando insert: %w", err)
	}

	return &Store{db: db, stmt: stmt}, nil
}

// migrate aplica alterações de schema em bancos criados por versões anteriores.
// É idempotente: pode rodar a cada Open sem efeitos colaterais.
func migrate(db *sql.DB) error {
	// Verifica se a coluna created_at já existe.
	rows, err := db.Query(`PRAGMA table_info(logs)`)
	if err != nil {
		return fmt.Errorf("lendo colunas: %w", err)
	}
	hasCreatedAt := false
	for rows.Next() {
		var cid int
		var name, ctype string
		var notnull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notnull, &dflt, &pk); err != nil {
			rows.Close()
			return fmt.Errorf("lendo coluna: %w", err)
		}
		if name == "created_at" {
			hasCreatedAt = true
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()

	if !hasCreatedAt {
		// ADD COLUMN no SQLite só aceita DEFAULT constante, por isso usamos ''
		// e em seguida preenchemos os registros antigos a partir de ingested_at.
		if _, err := db.Exec(`ALTER TABLE logs ADD COLUMN created_at TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("adicionando coluna created_at: %w", err)
		}
		if _, err := db.Exec(`UPDATE logs SET created_at = ingested_at WHERE created_at = ''`); err != nil {
			return fmt.Errorf("preenchendo created_at: %w", err)
		}
	}

	if _, err := db.Exec(`CREATE INDEX IF NOT EXISTS idx_logs_created_at ON logs(created_at)`); err != nil {
		return fmt.Errorf("criando índice created_at: %w", err)
	}
	return nil
}

// SaveBatch grava todas as entradas em uma única transação.
func (s *Store) SaveBatch(entries []Entry) error {
	if len(entries) == 0 {
		return nil
	}

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("iniciando transação: %w", err)
	}

	now := time.Now().UTC().Format(time.RFC3339Nano)
	stmt := tx.Stmt(s.stmt)
	for _, e := range entries {
		if _, err := stmt.Exec(
			nullable(e.Time), nullable(e.Level), nullable(e.Msg), nullable(e.Attrs), e.Raw, now, now,
		); err != nil {
			tx.Rollback()
			return fmt.Errorf("inserindo log: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// LogRow representa uma linha já persistida, lida de volta do banco.
type LogRow struct {
	ID         int64  `json:"id"`
	Time       string `json:"time"`
	Level      string `json:"level"`
	Msg        string `json:"msg"`
	Attrs      string `json:"attrs"`
	Raw        string `json:"raw"`
	IngestedAt string `json:"ingested_at"`
	CreatedAt  string `json:"created_at"`
}

// LogFilter descreve os critérios de busca usados pela interface web.
type LogFilter struct {
	Text    string // busca livre em msg/raw (LIKE)
	Level   string // filtra por nível exato (ex.: "ERROR")
	SinceID int64  // se > 0, retorna apenas linhas com id > SinceID (modo incremental)
	Limit   int    // máximo de linhas (default 200)
}

// QueryResult é o retorno genérico do console SQL: colunas + linhas em texto.
type QueryResult struct {
	Columns []string   `json:"columns"`
	Rows    [][]string `json:"rows"`
}

// FilterLogs retorna logs conforme o filtro. No modo incremental (SinceID > 0)
// as linhas vêm em ordem crescente de id; caso contrário, as mais recentes primeiro.
func (s *Store) FilterLogs(f LogFilter) ([]LogRow, error) {
	if f.Limit <= 0 || f.Limit > 5000 {
		f.Limit = 200
	}

	var where []string
	var args []any
	if f.Text != "" {
		where = append(where, "(msg LIKE ? OR raw LIKE ?)")
		like := "%" + f.Text + "%"
		args = append(args, like, like)
	}
	if f.Level != "" {
		where = append(where, "level = ?")
		args = append(args, f.Level)
	}
	if f.SinceID > 0 {
		where = append(where, "id > ?")
		args = append(args, f.SinceID)
	}

	query := "SELECT id, time, level, msg, attrs, raw, ingested_at, created_at FROM logs"
	if len(where) > 0 {
		query += " WHERE " + strings.Join(where, " AND ")
	}
	// Incremental: ordem natural (mais antigos primeiro) para anexar no fim da lista.
	// Carga inicial: mais recentes primeiro.
	if f.SinceID > 0 {
		query += " ORDER BY id ASC"
	} else {
		query += " ORDER BY id DESC"
	}
	query += " LIMIT ?"
	args = append(args, f.Limit)

	rows, err := s.db.Query(query, args...)
	if err != nil {
		return nil, fmt.Errorf("consultando logs: %w", err)
	}
	defer rows.Close()

	var out []LogRow
	for rows.Next() {
		var r LogRow
		var t, level, msg, attrs sql.NullString
		if err := rows.Scan(&r.ID, &t, &level, &msg, &attrs, &r.Raw, &r.IngestedAt, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("lendo log: %w", err)
		}
		r.Time, r.Level, r.Msg, r.Attrs = t.String, level.String, msg.String, attrs.String
		out = append(out, r)
	}
	return out, rows.Err()
}

// Levels devolve os níveis distintos presentes no banco, para popular o filtro.
func (s *Store) Levels() ([]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT level FROM logs WHERE level <> '' AND level IS NOT NULL ORDER BY level`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var levels []string
	for rows.Next() {
		var l string
		if err := rows.Scan(&l); err != nil {
			return nil, err
		}
		levels = append(levels, l)
	}
	return levels, rows.Err()
}

// errWriteQuery sinaliza que o SQL informado não é uma consulta de leitura.
var errWriteQuery = errors.New("apenas consultas de leitura (SELECT/WITH) são permitidas")

// readOnlyPrefixes são os comandos aceitos pelo console SQL.
var readOnlyPrefixes = []string{"SELECT", "WITH", "EXPLAIN", "PRAGMA"}

// RunQuery executa uma consulta de leitura arbitrária e devolve colunas + linhas.
// Recusa qualquer comando que não seja de leitura para proteger o banco.
func (s *Store) RunQuery(query string) (*QueryResult, error) {
	trimmed := strings.TrimSpace(query)
	// Bloqueia múltiplos statements separados por ';' (exceto um ';' final).
	if i := strings.IndexByte(trimmed, ';'); i >= 0 && strings.TrimSpace(trimmed[i+1:]) != "" {
		return nil, errors.New("apenas uma consulta por vez é permitida")
	}
	upper := strings.ToUpper(trimmed)
	allowed := false
	for _, p := range readOnlyPrefixes {
		if strings.HasPrefix(upper, p) {
			allowed = true
			break
		}
	}
	if !allowed {
		return nil, errWriteQuery
	}

	rows, err := s.db.Query(trimmed)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return nil, err
	}

	res := &QueryResult{Columns: cols, Rows: [][]string{}}
	for rows.Next() {
		raw := make([]any, len(cols))
		ptrs := make([]any, len(cols))
		for i := range raw {
			ptrs[i] = &raw[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		line := make([]string, len(cols))
		for i, v := range raw {
			line[i] = valueToString(v)
		}
		res.Rows = append(res.Rows, line)
	}
	return res, rows.Err()
}

// valueToString converte um valor genérico do SQLite para texto exibível.
func valueToString(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case []byte:
		return string(x)
	case string:
		return x
	default:
		return fmt.Sprintf("%v", x)
	}
}

// Close libera os recursos do banco.
func (s *Store) Close() error {
	if s.stmt != nil {
		s.stmt.Close()
	}
	return s.db.Close()
}

// nullable converte string vazia em NULL no SQLite, para não poluir as colunas.
func nullable(v string) any {
	if v == "" {
		return nil
	}
	return v
}
