package main

import (
	"log/slog"
	"net/http"
	"os"

	"k8s_ingestor/internal/pipeline"
)

func main() {
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})
	slog.SetDefault(slog.New(h))

	storePath := os.Getenv("PIPELINE_STORE_PATH")
	if storePath == "" {
		storePath = "/etc/k8s-ingestor/pipelines.json"
	}

	p := pipeline.NewHandler(storePath)

	mux := http.NewServeMux()

	api := http.NewServeMux()
	api.HandleFunc("/pipelines", p.ListPipelines)
	api.HandleFunc("/pipelines/create", p.CreatePipeline)
	api.HandleFunc("/pipelines/get", p.GetPipeline)
	api.HandleFunc("/pipelines/update", p.UpdatePipeline)
	api.HandleFunc("/pipelines/delete", p.DeletePipeline)
	api.HandleFunc("/pipelines/config", p.GenerateFluentBitConfig)
	api.HandleFunc("/health", p.Health)

	mux.Handle("/api/", http.StripPrefix("/api", api))
	mux.HandleFunc("/", serveUI)

	addr := os.Getenv("SERVER_ADDR")
	if addr == "" {
		addr = ":8082"
	}

	slog.Info("pipeline-service starting", "addr", addr, "store", storePath)
	if err := http.ListenAndServe(addr, mux); err != nil {
		slog.Error("server error", "error", err)
	}
}

func serveUI(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path == "/" {
		http.ServeFile(w, r, "web/pipeline/index.html")
		return
	}
	http.ServeFile(w, r, "web/pipeline/"+r.URL.Path)
}
