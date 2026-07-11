package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/foreman/foreman/internal/config"
	"github.com/foreman/foreman/internal/core"
)

func main() {
	configPath := flag.String("config", "foreman.yaml", "path to configuration file")
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

	if err := run(ctx, *configPath); err != nil {
		log.Fatalf("foreman: %v", err)
	}
}

func run(ctx context.Context, configPath string) error {
	cfg, err := config.Load(configPath)
	if err != nil {
		return err
	}

	app, err := core.Bootstrap(ctx, cfg)
	if err != nil {
		return err
	}
	defer app.Shutdown(ctx)

	log.Println("foreman started")
	<-ctx.Done()
	log.Println("shutting down")
	return nil
}
