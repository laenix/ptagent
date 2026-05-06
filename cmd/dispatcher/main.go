package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/ptagent/ptagent/internal/config"
	"github.com/ptagent/ptagent/internal/dispatcher"
)

func main() {
	configPath := flag.String("config", "./configs/dispatch.yaml", "dispatcher config file path")
	flag.Parse()

	cfg, err := config.LoadDispatchConfig(*configPath)
	if err != nil {
		log.Fatalf("load config: %v", err)
	}

	d, err := dispatcher.New(cfg)
	if err != nil {
		log.Fatalf("init dispatcher: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		quit := make(chan os.Signal, 1)
		signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
		<-quit
		log.Println("Received shutdown signal")
		cancel()
	}()

	log.Println("PTAgent Dispatcher starting...")
	if err := d.Run(ctx); err != nil {
		log.Fatalf("dispatcher run: %v", err)
	}
}
