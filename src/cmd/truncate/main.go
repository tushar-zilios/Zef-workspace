package main

import (
	"context"
	"fmt"
	"log"
	"time"

	"workspace/src/internal/config"
	"workspace/src/internal/db"
)

func main() {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	cfg, err := config.Load()
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	pool, err := db.InitDB(ctx, cfg.DatabaseURL)
	if err != nil {
		log.Fatalf("Failed to connect to database: %v", err)
	}
	defer db.CloseDB()

	truncateQuery := `
		DO $$
		DECLARE
			r RECORD;
		BEGIN
			FOR r IN (SELECT tablename FROM pg_tables WHERE schemaname = 'public')
			LOOP
				EXECUTE 'TRUNCATE TABLE ' || quote_ident(r.tablename) || ' RESTART IDENTITY CASCADE';
			END LOOP;
		END $$;
	`

	fmt.Println("Truncating all tables in Workspace database...")
	_, err = pool.Exec(ctx, truncateQuery)
	if err != nil {
		log.Fatalf("Failed to truncate tables: %v", err)
	}

	fmt.Println("Workspace Database truncated successfully.")
}
