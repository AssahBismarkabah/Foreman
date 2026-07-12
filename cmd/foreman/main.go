package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/foreman/foreman/internal/config"
	"github.com/foreman/foreman/internal/core"
)

// version is set at build time via ldflags (see Makefile or Dockerfile).
// Default "dev" so builds from source work without extra flags.
var version = "dev"

func main() {
	configPath := flag.String("config", "foreman.yaml", "path to configuration file")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Printf("foreman %s\n", version)
		os.Exit(0)
	}

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
