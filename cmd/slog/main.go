// Command slog lê logs JSON do stdin e os persiste em um banco SQLite,
// expondo também uma interface web para visualizar e consultar os logs.
//
// Uso típico (pipe a partir de outro processo):
//
//	meu-app | slog -db logs.db
//
// A interface web sobe junto em http://localhost:8080 (configurável com -web).
// Para desligá-la, use -web "".
//
// Para também repassar as linhas adiante no pipe, use -echo:
//
//	meu-app | slog -echo | grep ERROR
//
// Subcomandos:
//
//	slog db:clear            remove os arquivos .db (e -wal/-shm) do diretório atual
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/IsaacDSC/slog/internal/ingest"
	"github.com/IsaacDSC/slog/internal/store"
	"github.com/IsaacDSC/slog/internal/web"
)

func main() {
	// Despacha subcomandos no estilo "db:clear" antes do parsing de flags do
	// modo de ingest. Um subcomando é o primeiro argumento e contém ':'.
	if len(os.Args) > 1 && strings.Contains(os.Args[1], ":") {
		if err := runSubcommand(os.Args[1], os.Args[2:]); err != nil {
			fmt.Fprintf(os.Stderr, "slog: %v\n", err)
			os.Exit(1)
		}
		return
	}

	dbPath := flag.String("db", "logs.db", "caminho do arquivo SQLite")
	batch := flag.Int("batch", 100, "quantidade de registros por gravação em lote")
	flush := flag.Duration("flush", time.Second, "intervalo máximo para gravar um lote parcial")
	echo := flag.Bool("echo", false, "repassa cada linha para o stdout (passthrough no pipe)")
	webAddr := flag.String("web", ":8080", "endereço da interface web (vazio para desligar)")
	flag.Parse()

	if err := run(*dbPath, *batch, *flush, *echo, *webAddr); err != nil {
		fmt.Fprintf(os.Stderr, "slog: %v\n", err)
		os.Exit(1)
	}
}

// runSubcommand encaminha subcomandos no formato "namespace:ação".
func runSubcommand(name string, args []string) error {
	switch name {
	case "db:clear":
		return dbClear(args)
	default:
		return fmt.Errorf("subcomando desconhecido: %q", name)
	}
}

// dbClear remove os arquivos SQLite (.db) e seus arquivos auxiliares (-wal/-shm)
// de um diretório. Por padrão pede confirmação antes de apagar.
func dbClear(args []string) error {
	fs := flag.NewFlagSet("db:clear", flag.ExitOnError)
	dir := fs.String("dir", ".", "diretório onde procurar os arquivos .db")
	force := fs.Bool("f", false, "apaga sem pedir confirmação")
	fs.Parse(args)

	dbs, err := filepath.Glob(filepath.Join(*dir, "*.db"))
	if err != nil {
		return fmt.Errorf("procurando arquivos .db: %w", err)
	}
	sort.Strings(dbs)

	// Reúne cada .db com seus arquivos auxiliares do WAL, se existirem.
	var targets []string
	for _, db := range dbs {
		targets = append(targets, db)
		for _, suffix := range []string{"-wal", "-shm"} {
			side := db + suffix
			if _, err := os.Stat(side); err == nil {
				targets = append(targets, side)
			}
		}
	}

	if len(targets) == 0 {
		fmt.Fprintf(os.Stderr, "slog: nenhum arquivo .db encontrado em %s\n", *dir)
		return nil
	}

	fmt.Fprintln(os.Stderr, "slog: os seguintes arquivos serão removidos:")
	for _, t := range targets {
		fmt.Fprintf(os.Stderr, "  %s\n", t)
	}

	if !*force {
		fmt.Fprint(os.Stderr, "confirma a remoção? [y/N] ")
		resp, _ := bufio.NewReader(os.Stdin).ReadString('\n')
		switch strings.ToLower(strings.TrimSpace(resp)) {
		case "y", "yes", "s", "sim":
		default:
			fmt.Fprintln(os.Stderr, "slog: cancelado")
			return nil
		}
	}

	var removed int
	for _, t := range targets {
		if err := os.Remove(t); err != nil {
			return fmt.Errorf("removendo %s: %w", t, err)
		}
		removed++
	}
	fmt.Fprintf(os.Stderr, "slog: %d arquivo(s) removido(s)\n", removed)
	return nil
}

func run(dbPath string, batch int, flush time.Duration, echo bool, webAddr string) error {
	s, err := store.Open(dbPath)
	if err != nil {
		return err
	}
	defer s.Close()

	// Encerra de forma limpa em Ctrl+C / SIGTERM: fecha o stdin para o leitor
	// chegar ao EOF e gravar o lote pendente. quit sinaliza que o usuário pediu
	// para sair (vs. EOF natural do stdin).
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	quit := make(chan struct{})
	go func() {
		<-sig
		close(quit)
		os.Stdin.Close()
	}()

	// Servidor web opcional, rodando em paralelo ao ingest.
	var httpSrv *http.Server
	if webAddr != "" {
		httpSrv = &http.Server{Addr: webAddr, Handler: web.New(s).Handler()}
		go func() {
			fmt.Fprintf(os.Stderr, "slog: interface web em http://localhost%s\n", webAddr)
			if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				fmt.Fprintf(os.Stderr, "slog: web: %v\n", err)
			}
		}()
	}

	opts := ingest.Options{BatchSize: batch, FlushInterval: flush, Done: quit}
	if echo {
		opts.Echo = os.Stdout
	}

	total, ingestErr := ingest.Run(os.Stdin, s, opts)
	fmt.Fprintf(os.Stderr, "slog: %d linha(s) gravada(s) em %s\n", total, dbPath)

	// Após o EOF do ingest, mantém a interface web no ar para inspecionar os
	// logs; só encerra quando o usuário pedir (Ctrl+C / SIGTERM).
	if httpSrv != nil {
		select {
		case <-quit:
			// Ctrl+C durante o ingest: o usuário já quer sair.
		default:
			fmt.Fprintf(os.Stderr, "slog: ingest encerrado; interface web segue em http://localhost%s — Ctrl+C para sair\n", webAddr)
			<-quit
		}
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		httpSrv.Shutdown(ctx)
	}
	return ingestErr
}
