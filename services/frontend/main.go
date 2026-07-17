package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func main() {
	port := env("PORT", "3000")
	directory := env("FRONTEND_DIR", "frontend")
	absolute, err := filepath.Abs(directory)
	if err != nil {
		log.Fatal(err)
	}

	files := http.FileServer(http.Dir(absolute))
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_, _ = fmt.Fprint(w, `{"success":true,"message":"Frontend disponible","data":{"service":"frontend","status":"UP"}}`)
	})
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(filepath.Clean(r.URL.Path), "..") {
			http.Error(w, "ruta inválida", http.StatusBadRequest)
			return
		}
		files.ServeHTTP(w, r)
	}))

	server := &http.Server{
		Addr:              ":" + port,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       90 * time.Second,
	}
	log.Printf("frontend listening on http://localhost:%s from %s", port, absolute)
	log.Fatal(server.ListenAndServe())
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
