CREATE EXTENSION IF NOT EXISTS pgcrypto;

CREATE TABLE profiles (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id TEXT NOT NULL UNIQUE,
    bedtime TEXT NOT NULL DEFAULT '23:00',
    wake_time TEXT NOT NULL DEFAULT '07:30',
    persona TEXT NOT NULL DEFAULT 'gentle',
    reminder_style TEXT NOT NULL DEFAULT 'gentle',
    default_guidance TEXT NOT NULL DEFAULT 'rain',
    white_noise_duration_min INTEGER NOT NULL DEFAULT 20 CHECK (white_noise_duration_min IN (10, 20, 30)),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE night_sessions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id TEXT NOT NULL,
    date DATE NOT NULL,
    phase TEXT NOT NULL DEFAULT 'WAITING_TO_LOCK',
    resume_phase TEXT NOT NULL DEFAULT '',
    box_closed BOOLEAN NOT NULL DEFAULT FALSE,
    conversation_turns INTEGER NOT NULL DEFAULT 0 CHECK (conversation_turns BETWEEN 0 AND 3),
    selected_guidance TEXT NOT NULL DEFAULT '',
    audio_playing BOOLEAN NOT NULL DEFAULT FALSE,
    sunrise_progress INTEGER NOT NULL DEFAULT 0 CHECK (sunrise_progress BETWEEN 0 AND 100),
    paused_for_tonight BOOLEAN NOT NULL DEFAULT FALSE,
    latest_ai_draft JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    UNIQUE (user_id, date)
);
CREATE INDEX idx_night_sessions_user_updated ON night_sessions (user_id, updated_at DESC);

CREATE TABLE conversation_turns (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id UUID NOT NULL REFERENCES night_sessions(id) ON DELETE CASCADE,
    role TEXT NOT NULL CHECK (role IN ('user', 'assistant')),
    text TEXT NOT NULL,
    turn_index INTEGER NOT NULL CHECK (turn_index BETWEEN 1 AND 3),
    fallback BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_conversation_turns_session_created ON conversation_turns (session_id, created_at);

CREATE TABLE memory_cards (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    session_id UUID NOT NULL UNIQUE REFERENCES night_sessions(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL,
    date DATE NOT NULL,
    emotion TEXT NOT NULL,
    worry TEXT NOT NULL,
    tomorrow_task TEXT NOT NULL,
    comfort TEXT NOT NULL,
    suggested_guidance TEXT NOT NULL,
    fallback BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_memory_cards_user_date ON memory_cards (user_id, date DESC);

CREATE TABLE device_events (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    event_id TEXT NOT NULL UNIQUE,
    device_id TEXT NOT NULL,
    user_id TEXT NOT NULL,
    type TEXT NOT NULL,
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    occurred_at TIMESTAMPTZ NOT NULL,
    processed_at TIMESTAMPTZ NOT NULL,
    result JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_device_events_device_created ON device_events (device_id, created_at DESC);

CREATE TABLE device_commands (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    device_id TEXT NOT NULL,
    user_id TEXT NOT NULL,
    type TEXT NOT NULL,
    payload JSONB NOT NULL DEFAULT '{}'::jsonb,
    status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'dispatched', 'acked', 'failed')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    dispatched_at TIMESTAMPTZ,
    acked_at TIMESTAMPTZ,
    ack_payload JSONB NOT NULL DEFAULT '{}'::jsonb
);
CREATE INDEX idx_device_command_queue ON device_commands (device_id, status, created_at);
