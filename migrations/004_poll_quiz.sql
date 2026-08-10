-- 004_poll_quiz.sql
-- Quiz polls: a poll can be marked as a quiz, in which case one or more of its options
-- are flagged as the correct answer.

ALTER TABLE public.polls ADD COLUMN IF NOT EXISTS is_quiz BOOLEAN NOT NULL DEFAULT FALSE;
ALTER TABLE public.poll_options ADD COLUMN IF NOT EXISTS is_correct BOOLEAN NOT NULL DEFAULT FALSE;
