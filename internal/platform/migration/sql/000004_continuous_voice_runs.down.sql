ALTER TABLE night_sessions DROP CONSTRAINT IF EXISTS night_sessions_conversation_turns_check;
ALTER TABLE night_sessions
    ADD CONSTRAINT night_sessions_conversation_turns_check
        CHECK (conversation_turns BETWEEN 0 AND 3) NOT VALID;

DROP INDEX IF EXISTS idx_memory_cards_run;
DROP INDEX IF EXISTS idx_memory_cards_session;
ALTER TABLE memory_cards
    DROP CONSTRAINT IF EXISTS fk_memory_cards_run,
    DROP COLUMN IF EXISTS run_id;
ALTER TABLE memory_cards
    ADD CONSTRAINT memory_cards_session_id_key UNIQUE (session_id);

DROP INDEX IF EXISTS idx_conversation_turns_client_request;
DROP INDEX IF EXISTS idx_conversation_turns_run_created;
ALTER TABLE conversation_turns DROP CONSTRAINT IF EXISTS conversation_turns_turn_index_check;
ALTER TABLE conversation_turns
    ADD CONSTRAINT conversation_turns_turn_index_check
        CHECK (turn_index BETWEEN 1 AND 3) NOT VALID;
ALTER TABLE conversation_turns
    DROP CONSTRAINT IF EXISTS fk_conversation_turns_run,
    DROP COLUMN IF EXISTS run_id;
CREATE UNIQUE INDEX idx_conversation_turns_client_request
    ON conversation_turns (session_id, client_request_id, role)
    WHERE client_request_id IS NOT NULL;

DROP TABLE IF EXISTS conversation_runs;
