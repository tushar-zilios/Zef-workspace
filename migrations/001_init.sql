-- 001_init.sql
-- Consolidated baseline schema for Zef-workspace, generated from the inline
-- CREATE TABLE / ALTER TABLE / CREATE INDEX statements previously executed
-- at runtime by src/internal/db/db.go (InitDB). This file is idempotent
-- (IF NOT EXISTS throughout) and safe to run against an existing database.

-- =========================================================================
-- Workspaces
-- =========================================================================

CREATE TABLE IF NOT EXISTS public.workspaces (
    workspace_id UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    name         TEXT        NOT NULL,
    owner_id     TEXT        NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_workspaces_owner_id ON public.workspaces (owner_id);

-- =========================================================================
-- Conversations
-- =========================================================================

CREATE TABLE IF NOT EXISTS public.conversations (
    conversation_id UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    workspace_id    UUID        NOT NULL REFERENCES public.workspaces(workspace_id) ON DELETE CASCADE,
    type            TEXT        NOT NULL CHECK (type IN ('direct', 'group')),
    name            TEXT,
    created_by      TEXT        NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_conversations_workspace_id ON public.conversations (workspace_id);

-- =========================================================================
-- Conversation members
-- =========================================================================

CREATE TABLE IF NOT EXISTS public.conversation_members (
    conversation_id UUID        NOT NULL REFERENCES public.conversations(conversation_id) ON DELETE CASCADE,
    user_id         TEXT        NOT NULL,
    joined_at       TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    last_read_at    TIMESTAMPTZ NOT NULL DEFAULT '-infinity',
    PRIMARY KEY (conversation_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_conversation_members_user_id ON public.conversation_members (user_id);

-- =========================================================================
-- Messages
-- =========================================================================

CREATE TABLE IF NOT EXISTS public.messages (
    message_id      UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    conversation_id UUID        NOT NULL REFERENCES public.conversations(conversation_id) ON DELETE CASCADE,
    sender_id       TEXT        NOT NULL,
    sender_name     TEXT        NOT NULL,
    body            TEXT        NOT NULL,
    created_at      TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_messages_conversation_id_created_at ON public.messages (conversation_id, created_at DESC);

-- Attachment support
ALTER TABLE public.messages ADD COLUMN IF NOT EXISTS attachment_key TEXT;
ALTER TABLE public.messages ADD COLUMN IF NOT EXISTS attachment_url TEXT;
ALTER TABLE public.messages ADD COLUMN IF NOT EXISTS attachment_kind TEXT;
ALTER TABLE public.messages ADD COLUMN IF NOT EXISTS attachment_name TEXT;
ALTER TABLE public.messages ADD COLUMN IF NOT EXISTS attachment_size_bytes BIGINT;

-- Editing / soft-delete / scheduling
ALTER TABLE public.messages ADD COLUMN IF NOT EXISTS updated_at TIMESTAMPTZ;
ALTER TABLE public.messages ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;
ALTER TABLE public.messages ADD COLUMN IF NOT EXISTS scheduled_for TIMESTAMPTZ;
ALTER TABLE public.messages ADD COLUMN IF NOT EXISTS status TEXT NOT NULL DEFAULT 'sent';

CREATE INDEX IF NOT EXISTS idx_messages_scheduled_due ON public.messages (scheduled_for) WHERE status = 'scheduled';

-- View-once messages
ALTER TABLE public.messages ADD COLUMN IF NOT EXISTS view_once BOOLEAN NOT NULL DEFAULT FALSE;

-- Forwarding
ALTER TABLE public.messages ADD COLUMN IF NOT EXISTS forwarded_from_message_id UUID REFERENCES public.messages(message_id) ON DELETE SET NULL;
ALTER TABLE public.messages ADD COLUMN IF NOT EXISTS forwarded_from_sender_id TEXT;

-- Threading
ALTER TABLE public.messages ADD COLUMN IF NOT EXISTS thread_root_message_id UUID REFERENCES public.messages(message_id) ON DELETE CASCADE;
CREATE INDEX IF NOT EXISTS idx_messages_thread_root_id ON public.messages (thread_root_message_id) WHERE thread_root_message_id IS NOT NULL;

-- Shared task references (task metadata mirrored from another service)
ALTER TABLE public.messages ADD COLUMN IF NOT EXISTS shared_task_id TEXT;
ALTER TABLE public.messages ADD COLUMN IF NOT EXISTS shared_task_title TEXT;
ALTER TABLE public.messages ADD COLUMN IF NOT EXISTS shared_task_status TEXT;
ALTER TABLE public.messages ADD COLUMN IF NOT EXISTS shared_task_number INTEGER;

-- =========================================================================
-- Message reactions
-- =========================================================================

CREATE TABLE IF NOT EXISTS public.message_reactions (
    message_id UUID        NOT NULL REFERENCES public.messages(message_id) ON DELETE CASCADE,
    user_id    TEXT        NOT NULL,
    emoji      TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (message_id, user_id, emoji)
);

CREATE INDEX IF NOT EXISTS idx_message_reactions_message_id ON public.message_reactions (message_id);

-- =========================================================================
-- Message views (read receipts)
-- =========================================================================

CREATE TABLE IF NOT EXISTS public.message_views (
    message_id UUID        NOT NULL REFERENCES public.messages(message_id) ON DELETE CASCADE,
    user_id    TEXT        NOT NULL,
    viewed_at  TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (message_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_message_views_message_id ON public.message_views (message_id);
