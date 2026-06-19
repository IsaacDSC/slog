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
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/IsaacDSC/slog/internal/ingest"
	"github.com/IsaacDSC/slog/internal/store"
	"github.com/IsaacDSC/slog/internal/web"
)

func main() {
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

	opts := ingest.Options{BatchSize: batch, FlushInterval: flush}
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
