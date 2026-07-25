CREATE TABLE conversation_runs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id TEXT NOT NULL,
    device_id TEXT NOT NULL DEFAULT '',
    night_session_id UUID NOT NULL REFERENCES night_sessions(id) ON DELETE CASCADE,
    date DATE NOT NULL,
    status TEXT NOT NULL DEFAULT 'active'
        CHECK (status IN ('active', 'finishing', 'completed', 'aborted')),
    completed_turns INTEGER NOT NULL DEFAULT 0 CHECK (completed_turns >= 0),
    processing_turn_id TEXT,
    finish_event_id TEXT,
    guidance TEXT NOT NULL DEFAULT '',
    guidance_status TEXT NOT NULL DEFAULT 'pending'
        CHECK (guidance_status IN ('pending', 'playing', 'interrupted', 'completed')),
    started_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    finished_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_conversation_runs_user_date_created
    ON conversation_runs (user_id, date DESC, created_at DESC);
CREATE INDEX idx_conversation_runs_device_created
    ON conversation_runs (device_id, created_at DESC);
CREATE UNIQUE INDEX idx_conversation_runs_active_device
    ON conversation_runs (user_id, device_id)
    WHERE status IN ('active', 'finishing') AND device_id <> '';
CREATE UNIQUE INDEX idx_conversation_runs_finish_event
    ON conversation_runs (finish_event_id)
    WHERE finish_event_id IS NOT NULL;

INSERT INTO conversation_runs (
    id, user_id, device_id, night_session_id, date, status, completed_turns,
    guidance, guidance_status, started_at, finished_at, created_at, updated_at
)
SELECT
    gen_random_uuid(),
    session.user_id,
    '',
    session.id,
    session.date,
    CASE
        WHEN card.id IS NOT NULL THEN 'completed'
        WHEN session.phase IN ('CHOOSING_GUIDANCE', 'SLEEPING', 'SUNRISE', 'AWAKE') THEN 'completed'
        ELSE 'aborted'
    END,
    session.conversation_turns,
    COALESCE(NULLIF(session.selected_guidance, ''), ''),
    CASE WHEN card.id IS NOT NULL THEN 'completed' ELSE 'pending' END,
    COALESCE(session.conversation_started_at, session.created_at),
    CASE
        WHEN card.id IS NOT NULL OR session.phase IN ('CHOOSING_GUIDANCE', 'SLEEPING', 'SUNRISE', 'AWAKE')
            THEN session.updated_at
        ELSE NULL
    END,
    session.created_at,
    session.updated_at
FROM night_sessions AS session
LEFT JOIN memory_cards AS card ON card.session_id = session.id;

ALTER TABLE conversation_turns ADD COLUMN run_id UUID;
UPDATE conversation_turns AS turn
SET run_id = run.id
FROM conversation_runs AS run
WHERE run.night_session_id = turn.session_id;
ALTER TABLE conversation_turns
    ALTER COLUMN run_id SET NOT NULL,
    ADD CONSTRAINT fk_conversation_turns_run
        FOREIGN KEY (run_id) REFERENCES conversation_runs(id) ON DELETE CASCADE;
CREATE INDEX idx_conversation_turns_run_created
    ON conversation_turns (run_id, created_at);
DROP INDEX IF EXISTS idx_conversation_turns_client_request;
CREATE UNIQUE INDEX idx_conversation_turns_client_request
    ON conversation_turns (run_id, client_request_id, role)
    WHERE client_request_id IS NOT NULL;
ALTER TABLE conversation_turns DROP CONSTRAINT IF EXISTS conversation_turns_turn_index_check;
ALTER TABLE conversation_turns
    ADD CONSTRAINT conversation_turns_turn_index_check CHECK (turn_index >= 1);

ALTER TABLE memory_cards ADD COLUMN run_id UUID;
UPDATE memory_cards AS card
SET run_id = run.id
FROM conversation_runs AS run
WHERE run.night_session_id = card.session_id;
ALTER TABLE memory_cards
    ALTER COLUMN run_id SET NOT NULL,
    ADD CONSTRAINT fk_memory_cards_run
        FOREIGN KEY (run_id) REFERENCES conversation_runs(id) ON DELETE CASCADE;
ALTER TABLE memory_cards DROP CONSTRAINT IF EXISTS memory_cards_session_id_key;
CREATE INDEX idx_memory_cards_session ON memory_cards (session_id);
CREATE UNIQUE INDEX idx_memory_cards_run ON memory_cards (run_id);

ALTER TABLE night_sessions DROP CONSTRAINT IF EXISTS night_sessions_conversation_turns_check;
ALTER TABLE night_sessions
    ADD CONSTRAINT night_sessions_conversation_turns_check CHECK (conversation_turns >= 0);
