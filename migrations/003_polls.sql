-- 003_polls.sql
-- Poll messages: a message can carry a poll (question + options), members vote for one
-- option (single-choice) or many (multi-choice) via poll_votes.

CREATE TABLE IF NOT EXISTS public.polls (
    poll_id      UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    message_id   UUID        NOT NULL REFERENCES public.messages(message_id) ON DELETE CASCADE,
    question     TEXT        NOT NULL,
    multi_choice BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE UNIQUE INDEX IF NOT EXISTS idx_polls_message_id ON public.polls (message_id);

CREATE TABLE IF NOT EXISTS public.poll_options (
    option_id    UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    poll_id      UUID        NOT NULL REFERENCES public.polls(poll_id) ON DELETE CASCADE,
    option_text  TEXT        NOT NULL,
    option_order INT         NOT NULL DEFAULT 0
);

CREATE INDEX IF NOT EXISTS idx_poll_options_poll_id ON public.poll_options (poll_id);

CREATE TABLE IF NOT EXISTS public.poll_votes (
    poll_id    UUID        NOT NULL REFERENCES public.polls(poll_id) ON DELETE CASCADE,
    option_id  UUID        NOT NULL REFERENCES public.poll_options(option_id) ON DELETE CASCADE,
    user_id    TEXT        NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (option_id, user_id)
);

CREATE INDEX IF NOT EXISTS idx_poll_votes_poll_id ON public.poll_votes (poll_id);
CREATE INDEX IF NOT EXISTS idx_poll_votes_user_id ON public.poll_votes (user_id);
