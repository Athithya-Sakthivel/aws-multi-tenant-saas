package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

	"auth/internal"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	app, err := internal.NewApp(ctx)
	if err != nil {
		log.Fatalf("init failed: %v", err)
	}

	mode := "serve"
	if len(os.Args) > 1 {
		mode = os.Args[1]
	}

	switch mode {
	case "migrate":
		if err := app.Migrate(ctx); err != nil {
			log.Fatalf("migration failed: %v", err)
		}
		log.Println("bootstrap migration complete")
	case "serve":
		if err := app.Run(ctx); err != nil {
			log.Fatalf("server failed: %v", err)
		}
	default:
		log.Fatalf("unknown mode: %s", mode)
	}
}
