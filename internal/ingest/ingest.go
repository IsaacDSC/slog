// Package ingest lê logs JSON de um io.Reader, normaliza e envia em lote ao store.
package ingest

import (
	"bufio"
	"encoding/json"
	"io"
	"time"

	"github.com/IsaacDSC/slog/internal/store"
)

// reservedKeys são os campos que extraímos para colunas próprias; o resto vira "attrs".
var reservedKeys = map[string]bool{"time": true, "level": true, "msg": true}

// Options controla o comportamento do ingestor.
type Options struct {
	BatchSize     int           // nº de registros antes de gravar
	FlushInterval time.Duration // tempo máximo antes de gravar um lote parcial
	Echo          io.Writer     // se != nil, repassa cada linha (passthrough no pipe)
	// Done, se != nil, interrompe a ingestão quando fechado (ex.: Ctrl+C),
	// gravando o lote pendente antes de retornar. Necessário porque fechar o
	// stdin não destrava uma leitura bloqueada quando ele é um terminal (TTY).
	Done <-chan struct{}
}

// Run consome r linha a linha até EOF, gravando os logs em s.
// Retorna o total de linhas processadas e o primeiro erro fatal, se houver.
func Run(r io.Reader, s *store.Store, opts Options) (int, error) {
	if opts.BatchSize <= 0 {
		opts.BatchSize = 100
	}
	if opts.FlushInterval <= 0 {
		opts.FlushInterval = time.Second
	}

	scanner := bufio.NewScanner(r)
	// Linhas de log podem ser grandes; aumenta o buffer máximo para 1 MiB.
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	batch := make([]store.Entry, 0, opts.BatchSize)
	ticker := time.NewTicker(opts.FlushInterval)
	defer ticker.Stop()

	// Linhas chegam por um canal para podermos intercalar com o ticker de flush.
	lines := make(chan string)
	scanErr := make(chan error, 1)
	go func() {
		for scanner.Scan() {
			lines <- scanner.Text()
		}
		scanErr <- scanner.Err()
		close(lines)
	}()

	total := 0
	flush := func() error {
		if len(batch) == 0 {
			return nil
		}
		if err := s.SaveBatch(batch); err != nil {
			return err
		}
		batch = batch[:0]
		return nil
	}

	for {
		select {
		case line, ok := <-lines:
			if !ok {
				// Canal fechado: stdin terminou. Grava o que sobrou.
				if err := flush(); err != nil {
					return total, err
				}
				return total, <-scanErr
			}
			if opts.Echo != nil {
				io.WriteString(opts.Echo, line+"\n")
			}
			if line == "" {
				continue
			}
			batch = append(batch, parseLine(line))
			total++
			if len(batch) >= opts.BatchSize {
				if err := flush(); err != nil {
					return total, err
				}
			}
		case <-ticker.C:
			if err := flush(); err != nil {
				return total, err
			}
		case <-opts.Done:
			// Saída pedida pelo usuário (Ctrl+C). Grava o lote pendente e
			// encerra mesmo que a leitura do stdin siga bloqueada — caso de
			// quando o stdin é um terminal e não chega ao EOF.
			return total, flush()
		}
	}
}

// parseLine transforma uma linha em Entry. Se não for JSON válido, guarda só o raw.
func parseLine(line string) store.Entry {
	e := store.Entry{Raw: line}

	var fields map[string]json.RawMessage
	if err := json.Unmarshal([]byte(line), &fields); err != nil {
		// Não é JSON: mantém a linha bruta, sem campos estruturados.
		return e
	}

	e.Time = asString(fields["time"])
	e.Level = asString(fields["level"])
	e.Msg = asString(fields["msg"])

	// Os campos restantes (atributos do slog) são reserializados como JSON.
	attrs := make(map[string]json.RawMessage)
	for k, v := range fields {
		if !reservedKeys[k] {
			attrs[k] = v
		}
	}
	if len(attrs) > 0 {
		if b, err := json.Marshal(attrs); err == nil {
			e.Attrs = string(b)
		}
	}
	return e
}

// asString extrai o valor como texto. Strings JSON são desempacotadas;
// outros tipos (número, bool) ficam na forma textual original.
func asString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return string(raw)
}
