package main

import (
	"log"
	"os"

	"github.com/baomian/baomian-backend/internal/config"
	"github.com/baomian/baomian-backend/internal/platform/migration"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		log.Fatal(err)
	}
	direction := "up"
	if len(os.Args) > 1 {
		direction = os.Args[1]
	}
	if err := migration.Run(cfg.DatabaseURL, direction, migration.Files); err != nil {
		log.Fatal(err)
	}
	log.Printf("migration %s completed", direction)
}
