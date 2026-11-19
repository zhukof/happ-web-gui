package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const maxRequestBytes = 16 << 10

type api struct {
	manager *Manager
}

func newAPI(manager *Manager, web http.Handler) http.Handler {
	a := api{manager: manager}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/status", a.status)
	mux.HandleFunc("GET /api/servers", a.servers)
	mux.HandleFunc("GET /api/subscriptions", a.subscriptions)
	mux.HandleFunc("POST /api/subscriptions", a.addSubscription)
	mux.HandleFunc("POST /api/subscriptions/use", a.useSubscription)
	mux.HandleFunc("POST /api/subscriptions/update", a.updateSubscriptions)
	mux.HandleFunc("POST /api/connect", a.connect)
	mux.HandleFunc("POST /api/disconnect", a.disconnect)
	mux.Handle("/", web)
	return mux
}

func (a api) status(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, a.manager.status())
}

func (a api) servers(w http.ResponseWriter, r *http.Request) {

	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	output, err := a.manager.cli.run(ctx, "list")
	if err != nil {
		fmt.Println(err)
		writeAPIError(w, err)
		return
	}
	fmt.Println(http.StatusOK)
	writeJSON(w, http.StatusOK, map[string]any{"servers": parseServers(output), "raw": output})
}

func (a api) subscriptions(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	output, err := a.manager.cli.run(ctx, "sub", "list")
	if err != nil {
		writeAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"subscriptions": parseSubscriptions(output), "raw": output})
}

func (a api) addSubscription(w http.ResponseWriter, r *http.Request) {
	var req struct {
		URL  string `json:"url"`
		Name string `json:"name"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeAPIError(w, err)
		return
	}
	parsed, err := url.Parse(req.URL)
	if err != nil || parsed.Scheme != "https" || parsed.Hostname() == "" || strings.TrimSpace(req.Name) == "" {
		writeAPIError(w, errors.New("name and HTTPS subscription URL required"))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	_, err = a.manager.cli.run(ctx, "sub", "add", req.URL, "--name", req.Name)
	writeResult(w, err)
}

func (a api) useSubscription(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name string `json:"name"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeAPIError(w, err)
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		writeAPIError(w, errors.New("name required"))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	writeResult(w, a.manager.useSubscription(ctx, req.Name))
}

func (a api) updateSubscriptions(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	_, err := a.manager.cli.run(ctx, "sub", "update")
	writeResult(w, err)
}

func (a api) connect(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Server string `json:"server"`
	}
	if err := decodeJSON(w, r, &req); err != nil {
		writeAPIError(w, err)
		return
	}
	writeResult(w, a.manager.Start(req.Server))
}

func (a api) disconnect(w http.ResponseWriter, _ *http.Request) {
	writeResult(w, a.manager.Stop())
}

func writeResult(w http.ResponseWriter, err error) {
	if err != nil {
		writeAPIError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}

func writeAPIError(w http.ResponseWriter, err error) {
	code := http.StatusBadRequest
	var maxBytesError *http.MaxBytesError
	if errors.As(err, &maxBytesError) {
		code = http.StatusRequestEntityTooLarge
	}
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

func writeJSON(w http.ResponseWriter, code int, data any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(data)
}

func decodeJSON(w http.ResponseWriter, r *http.Request, target any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(target); err != nil {
		return err
	}
	err := decoder.Decode(new(json.RawMessage))
	switch {
	case errors.Is(err, io.EOF):
		return nil
	case err == nil:
		return errors.New("request body must contain a single JSON value")
	default:
		return err
	}
}
