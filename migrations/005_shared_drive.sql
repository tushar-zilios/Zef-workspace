-- 005_shared_drive.sql
-- Shared Z-Drive file/folder references: a message can carry a preview of a Z-Drive
-- share link (public token minted by Zef-drive), mirroring the shared_task_* columns.

ALTER TABLE public.messages ADD COLUMN IF NOT EXISTS shared_drive_resource_type TEXT;
ALTER TABLE public.messages ADD COLUMN IF NOT EXISTS shared_drive_ref_id TEXT;
ALTER TABLE public.messages ADD COLUMN IF NOT EXISTS shared_drive_token TEXT;
ALTER TABLE public.messages ADD COLUMN IF NOT EXISTS shared_drive_name TEXT;
ALTER TABLE public.messages ADD COLUMN IF NOT EXISTS shared_drive_size_bytes BIGINT;
ALTER TABLE public.messages ADD COLUMN IF NOT EXISTS shared_drive_mime_type TEXT;
