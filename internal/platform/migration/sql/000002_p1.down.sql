DROP TABLE IF EXISTS devices;

DROP INDEX IF EXISTS idx_device_command_lease;
ALTER TABLE device_commands
    DROP COLUMN IF EXISTS lease_expires_at,
    DROP COLUMN IF EXISTS dispatch_attempts;

ALTER TABLE memory_cards
    DROP COLUMN IF EXISTS tomorrow_task_completed_at,
    DROP COLUMN IF EXISTS tomorrow_task_completed;

DROP INDEX IF EXISTS idx_conversation_turns_client_request;
ALTER TABLE conversation_turns
    DROP CONSTRAINT IF EXISTS chk_conversation_turns_input_mode,
    DROP COLUMN IF EXISTS client_request_id,
    DROP COLUMN IF EXISTS input_mode;

DROP INDEX IF EXISTS idx_night_sessions_audio_due;
DROP INDEX IF EXISTS idx_night_sessions_resume_due;
DROP INDEX IF EXISTS idx_night_sessions_conversation_due;
ALTER TABLE night_sessions
    DROP COLUMN IF EXISTS audio_ends_at,
    DROP COLUMN IF EXISTS resume_deadline_at,
    DROP COLUMN IF EXISTS phone_removed_at,
    DROP COLUMN IF EXISTS conversation_processing_until,
    DROP COLUMN IF EXISTS conversation_hard_deadline_at,
    DROP COLUMN IF EXISTS conversation_silence_deadline_at,
    DROP COLUMN IF EXISTS conversation_last_activity_at,
    DROP COLUMN IF EXISTS conversation_started_at,
    DROP COLUMN IF EXISTS finalize_reason,
    DROP COLUMN IF EXISTS reminders_skipped;

ALTER TABLE profiles
    DROP COLUMN IF EXISTS wake_alarm_enabled,
    DROP COLUMN IF EXISTS bedtime_reminder_enabled,
    DROP COLUMN IF EXISTS time_zone;
