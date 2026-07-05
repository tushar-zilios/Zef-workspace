package db

import (
	"context"
	"fmt"
	"sync"
	"time"

	"workspace/src/internal/logger"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

var (
	pool *pgxpool.Pool
	once sync.Once
)

func InitDB(ctx context.Context, dbURL string) (*pgxpool.Pool, error) {
	if dbURL == "" {
		logger.LogDB("DATABASE_URL not set; skipping DB initialization")
		return nil, nil
	}

	var err error
	once.Do(func() {
		logger.LogDB("Initializing workspace database pool...")

		config, parseErr := pgxpool.ParseConfig(dbURL)
		if parseErr != nil {
			err = fmt.Errorf("failed to parse workspace database URL: %w", parseErr)
			return
		}
		config.ConnConfig.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol

		retryErr := retryWithExponentialBackoff(ctx, 5, 1*time.Second, 30*time.Second, func() error {
			var connErr error
			pool, connErr = pgxpool.NewWithConfig(ctx, config)
			if connErr != nil {
				return fmt.Errorf("failed to connect to workspace database: %w", connErr)
			}
			if pingErr := pool.Ping(ctx); pingErr != nil {
				pool.Close()
				pool = nil
				return fmt.Errorf("failed to ping workspace database: %w", pingErr)
			}
			return nil
		}, func(format string, args ...any) {
			logger.LogDB(format, args...)
		})

		if retryErr != nil {
			err = fmt.Errorf("workspace database initialization failed after retries: %w", retryErr)
			return
		}
		logger.LogDB("Workspace DB connection pool initialized successfully.")

		_, execErr := pool.Exec(ctx, `
			CREATE TABLE IF NOT EXISTS public.workspaces (
				workspace_id UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
				name         TEXT        NOT NULL,
				owner_id     TEXT        NOT NULL,
				created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
			);
		`)
		if execErr != nil {
			logger.LogDB("Warning: failed to create workspaces table: %v", execErr)
		}

		_, _ = pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_workspaces_owner_id ON public.workspaces (owner_id);`)

		_, execErr = pool.Exec(ctx, `
			CREATE TABLE IF NOT EXISTS public.conversations (
				conversation_id UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
				workspace_id    UUID        NOT NULL REFERENCES public.workspaces(workspace_id) ON DELETE CASCADE,
				type            TEXT        NOT NULL CHECK (type IN ('direct', 'group')),
				name            TEXT,
				created_by      TEXT        NOT NULL,
				created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
			);
		`)
		if execErr != nil {
			logger.LogDB("Warning: failed to create conversations table: %v", execErr)
		}
		_, _ = pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_conversations_workspace_id ON public.conversations (workspace_id);`)

		_, execErr = pool.Exec(ctx, `
			CREATE TABLE IF NOT EXISTS public.conversation_members (
				conversation_id UUID        NOT NULL REFERENCES public.conversations(conversation_id) ON DELETE CASCADE,
				user_id         TEXT        NOT NULL,
				joined_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				PRIMARY KEY (conversation_id, user_id)
			);
		`)
		if execErr != nil {
			logger.LogDB("Warning: failed to create conversation_members table: %v", execErr)
		}
		_, _ = pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_conversation_members_user_id ON public.conversation_members (user_id);`)

		_, execErr = pool.Exec(ctx, `
			CREATE TABLE IF NOT EXISTS public.messages (
				message_id      UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
				conversation_id UUID        NOT NULL REFERENCES public.conversations(conversation_id) ON DELETE CASCADE,
				sender_id       TEXT        NOT NULL,
				body            TEXT        NOT NULL,
				created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
			);
		`)
		if execErr != nil {
			logger.LogDB("Warning: failed to create messages table: %v", execErr)
		}
		_, _ = pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_messages_conversation_id_created_at ON public.messages (conversation_id, created_at DESC);`)

		_, _ = pool.Exec(ctx, `ALTER TABLE public.messages ADD COLUMN IF NOT EXISTS attachment_key TEXT;`)
		_, _ = pool.Exec(ctx, `ALTER TABLE public.messages ADD COLUMN IF NOT EXISTS attachment_url TEXT;`)
		_, _ = pool.Exec(ctx, `ALTER TABLE public.messages ADD COLUMN IF NOT EXISTS attachment_kind TEXT;`)
		_, _ = pool.Exec(ctx, `ALTER TABLE public.messages ADD COLUMN IF NOT EXISTS attachment_name TEXT;`)
		_, _ = pool.Exec(ctx, `ALTER TABLE public.messages ADD COLUMN IF NOT EXISTS attachment_size_bytes BIGINT;`)
		_, _ = pool.Exec(ctx, `ALTER TABLE public.messages ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ;`)
		_, _ = pool.Exec(ctx, `ALTER TABLE public.messages ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;`)
		_, _ = pool.Exec(ctx, `ALTER TABLE public.messages ADD COLUMN IF NOT EXISTS scheduled_for TIMESTAMPTZ;`)
		_, _ = pool.Exec(ctx, `ALTER TABLE public.messages ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'sent';`)
		_, _ = pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_messages_scheduled_due ON public.messages (scheduled_for) WHERE status = 'scheduled';`)

		_, execErr = pool.Exec(ctx, `
			CREATE TABLE IF NOT EXISTS public.message_reactions (
				message_id UUID        NOT NULL REFERENCES public.messages(message_id) ON DELETE CASCADE,
				user_id    TEXT        NOT NULL,
				emoji      TEXT        NOT NULL,
				created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				PRIMARY KEY (message_id, user_id, emoji)
			);
		`)
		if execErr != nil {
			logger.LogDB("Warning: failed to create message_reactions table: %v", execErr)
		}
		_, _ = pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_message_reactions_message_id ON public.message_reactions (message_id);`)

		_, _ = pool.Exec(ctx, `ALTER TABLE public.messages ADD COLUMN IF NOT EXISTS view_once BOOLEAN NOT NULL DEFAULT FALSE;`)

		_, execErr = pool.Exec(ctx, `
			CREATE TABLE IF NOT EXISTS public.message_views (
				message_id UUID        NOT NULL REFERENCES public.messages(message_id) ON DELETE CASCADE,
				user_id    TEXT        NOT NULL,
				viewed_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
				PRIMARY KEY (message_id, user_id)
			);
		`)
		if execErr != nil {
			logger.LogDB("Warning: failed to create message_views table: %v", execErr)
		}
		_, _ = pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_message_views_message_id ON public.message_views (message_id);`)

		_, _ = pool.Exec(ctx, `ALTER TABLE public.messages ADD COLUMN IF NOT EXISTS forwarded_from_message_id UUID REFERENCES public.messages(message_id) ON DELETE SET NULL;`)
		_, _ = pool.Exec(ctx, `ALTER TABLE public.messages ADD COLUMN IF NOT EXISTS forwarded_from_sender_id TEXT;`)

		_, _ = pool.Exec(ctx, `ALTER TABLE public.messages ADD COLUMN IF NOT EXISTS thread_root_message_id UUID REFERENCES public.messages(message_id) ON DELETE CASCADE;`)
		_, _ = pool.Exec(ctx, `CREATE INDEX IF NOT EXISTS idx_messages_thread_root_id ON public.messages (thread_root_message_id) WHERE thread_root_message_id IS NOT NULL;`)

		_, _ = pool.Exec(ctx, `ALTER TABLE public.messages ADD COLUMN IF NOT EXISTS shared_task_id TEXT;`)
		_, _ = pool.Exec(ctx, `ALTER TABLE public.messages ADD COLUMN IF NOT EXISTS shared_task_title TEXT;`)
		_, _ = pool.Exec(ctx, `ALTER TABLE public.messages ADD COLUMN IF NOT EXISTS shared_task_status TEXT;`)
		_, _ = pool.Exec(ctx, `ALTER TABLE public.messages ADD COLUMN IF NOT EXISTS shared_task_number INTEGER;`)
	})

	return pool, err
}

func GetPoolOrNil() *pgxpool.Pool {
	return pool
}

func PoolReady() bool {
	return pool != nil
}

func CloseDB() {
	if pool != nil {
		logger.LogDB("Closing workspace database connection pool.")
		pool.Close()
	}
}
