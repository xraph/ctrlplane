package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"os/signal"
	"time"

	"github.com/xraph/ctrlplane/api"
	"github.com/xraph/ctrlplane/app"
	"github.com/xraph/ctrlplane/provider/docker"
	"github.com/xraph/ctrlplane/store/memory"
)

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}

func run() error {
	memStore := memory.New()

	cp, err := app.New(
		app.WithStore(memStore),
		app.WithProvider("docker", docker.New(docker.Config{})),
		app.WithDefaultProvider("docker"),
	)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	if err := cp.Start(ctx); err != nil {
		return err
	}

	handler := api.New(cp).Handler()

	addr := ":8080"
	if port := os.Getenv("PORT"); port != "" {
		addr = ":" + port
	}

	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		<-ctx.Done()

		if shutdownErr := cp.Stop(context.Background()); shutdownErr != nil {
			log.Printf("shutdown error: %v", shutdownErr)
		}

		if closeErr := srv.Close(); closeErr != nil {
			log.Printf("server close error: %v", closeErr)
		}
	}()

	log.Printf("ctrlplane listening on %s", addr)

	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}

	return nil
}
