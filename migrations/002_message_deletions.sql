-- 002_message_deletions.sql
-- Per-user "delete for me" support: hides a message from one member's view
-- without soft-deleting it for the whole conversation (that remains
-- messages.deleted_at, used for "delete for everyone").

CREATE TABLE IF NOT EXISTS public.message_deletions (
    message_id UUID        NOT NULL REFERENCES public.messages(message_id) ON DELETE CASCADE,
    user_id    TEXT        NOT NULL,
    deleted_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (message_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_message_deletions_user_id ON public.message_deletions (user_id);
