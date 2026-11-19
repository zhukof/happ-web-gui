package main

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
)

//go:embed web/dist/*
var site embed.FS

func webHandler() (http.Handler, error) {
	if _, err := fs.Stat(site, "web/dist/index.html"); errors.Is(err, fs.ErrNotExist) {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/" {
				http.NotFound(w, r)
				return
			}
			http.Error(w, "Web UI unavailable: add web/dist and rebuild. The API is available at /api/status.", http.StatusServiceUnavailable)
		}), nil
	} else if err != nil {
		return nil, fmt.Errorf("load embedded web UI: %w", err)
	}
	static, err := fs.Sub(site, "web/dist")
	if err != nil {
		return nil, fmt.Errorf("load embedded web UI: %w", err)
	}
	return http.FileServer(http.FS(static)), nil
}
