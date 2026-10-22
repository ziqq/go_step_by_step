CREATE TABLE IF NOT EXISTS lesson28_messages (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    conversation_id text NOT NULL CHECK (length(conversation_id) BETWEEN 1 AND 80),
    body text NOT NULL CHECK (length(body) BETWEEN 1 AND 280),
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0)
);
