ALTER TABLE profiles
    ADD COLUMN time_zone TEXT NOT NULL DEFAULT 'Asia/Shanghai',
    ADD COLUMN bedtime_reminder_enabled BOOLEAN NOT NULL DEFAULT TRUE,
    ADD COLUMN wake_alarm_enabled BOOLEAN NOT NULL DEFAULT TRUE;

ALTER TABLE night_sessions
    ADD COLUMN reminders_skipped BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN finalize_reason TEXT NOT NULL DEFAULT '',
    ADD COLUMN conversation_started_at TIMESTAMPTZ,
    ADD COLUMN conversation_last_activity_at TIMESTAMPTZ,
    ADD COLUMN conversation_silence_deadline_at TIMESTAMPTZ,
    ADD COLUMN conversation_hard_deadline_at TIMESTAMPTZ,
    ADD COLUMN conversation_processing_until TIMESTAMPTZ,
    ADD COLUMN phone_removed_at TIMESTAMPTZ,
    ADD COLUMN resume_deadline_at TIMESTAMPTZ,
    ADD COLUMN audio_ends_at TIMESTAMPTZ;

CREATE INDEX idx_night_sessions_conversation_due
    ON night_sessions (conversation_silence_deadline_at)
    WHERE phase = 'CONVERSATION';
CREATE INDEX idx_night_sessions_resume_due
    ON night_sessions (resume_deadline_at)
    WHERE phase = 'PHONE_REMOVED';
CREATE INDEX idx_night_sessions_audio_due
    ON night_sessions (audio_ends_at)
    WHERE audio_playing = TRUE;

ALTER TABLE conversation_turns
    ADD COLUMN input_mode TEXT NOT NULL DEFAULT 'text',
    ADD COLUMN client_request_id TEXT;
ALTER TABLE conversation_turns
    ADD CONSTRAINT chk_conversation_turns_input_mode CHECK (input_mode IN ('text', 'voice'));
CREATE UNIQUE INDEX idx_conversation_turns_client_request
    ON conversation_turns (session_id, client_request_id, role)
    WHERE client_request_id IS NOT NULL;

ALTER TABLE memory_cards
    ADD COLUMN tomorrow_task_completed BOOLEAN NOT NULL DEFAULT FALSE,
    ADD COLUMN tomorrow_task_completed_at TIMESTAMPTZ;

ALTER TABLE device_commands
    ADD COLUMN dispatch_attempts INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN lease_expires_at TIMESTAMPTZ;
CREATE INDEX idx_device_command_lease
    ON device_commands (device_id, status, lease_expires_at, created_at);

CREATE TABLE devices (
    device_id TEXT PRIMARY KEY,
    user_id TEXT NOT NULL,
    firmware_version TEXT NOT NULL DEFAULT '',
    capabilities JSONB NOT NULL DEFAULT '{}'::jsonb,
    status JSONB NOT NULL DEFAULT '{}'::jsonb,
    local_time TIMESTAMPTZ,
    last_seen_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_devices_user_last_seen ON devices (user_id, last_seen_at DESC);
