package httpapi

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed console/*
var consoleFiles embed.FS

func registerConsole(mux *http.ServeMux) {
	files, err := fs.Sub(consoleFiles, "console")
	if err != nil {
		panic(err)
	}
	assets := http.FileServerFS(files)
	serve := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Cache-Control", "no-cache")
		assets.ServeHTTP(w, r)
	}
	mux.HandleFunc("GET /{$}", serve)
	mux.Handle("GET /console/", http.StripPrefix("/console", http.HandlerFunc(serve)))
}
