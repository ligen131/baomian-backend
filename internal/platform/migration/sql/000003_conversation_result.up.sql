ALTER TABLE conversation_turns
    ADD COLUMN result JSONB NOT NULL DEFAULT '{}'::jsonb;
