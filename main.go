package main

import (
	"log/slog"
	"net/http"
	"os"
)

func main() {
	setupLogger()

	port := os.Getenv("PORT")
	if port == "" {
		port = "3000"
	}

	s := newServer()
	r := newRouter(s)

	slog.Info("server starting", "port", port)
	if err := http.ListenAndServe(":"+port, r); err != nil {
		slog.Error("server stopped", "error", err)
		os.Exit(1)
	}
}
