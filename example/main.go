// Command example gera logs JSON contínuos em vários níveis, para testar o slog.
//
// Uso:
//
//	go run ./example | go run ./cmd/slog -db logs.db
//
// Cada linha sai no formato do log/slog (JSON com time/level/msg + atributos),
// que é exatamente o que o slog espera ingerir.
package main

import (
	"encoding/json"
	"log"
	"log/slog"
	"net/http"
	"os"
)

type Input struct {
	Level string         `json:"level"`
	Data  map[string]any `json:"data"`
	Msg   string         `json:"msg"`
}

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelDebug, // emite todos os níveis, inclusive DEBUG
	}))

	http.HandleFunc("POST /log", func(w http.ResponseWriter, r *http.Request) {
		defer r.Body.Close()
		var i Input
		if err := json.NewDecoder(r.Body).Decode(&i); err != nil {
			w.Write([]byte(err.Error()))
			return
		}

		var keyvalues []any

		for k, v := range i.Data {
			keyvalues = append(keyvalues, k)
			keyvalues = append(keyvalues, v)
		}

		switch i.Level {
		case "INFO":
			logger.Info(i.Msg, keyvalues...)
			w.WriteHeader(http.StatusCreated)
			return
		case "WARN":
			logger.Warn(i.Msg, keyvalues...)
			w.WriteHeader(http.StatusCreated)
			return
		case "ERROR":
			logger.Error(i.Msg, keyvalues...)
			w.WriteHeader(http.StatusCreated)
			return
		default:
			w.Write([]byte("Invalid log level"))
			w.WriteHeader(http.StatusBadRequest)
			return
		}
	})

	if err := http.ListenAndServe(":3333", nil); err != nil {
		log.Fatal(err.Error())
	}
}
