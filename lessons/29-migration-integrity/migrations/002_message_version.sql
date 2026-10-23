ALTER TABLE lesson29_messages
    ADD COLUMN version bigint NOT NULL DEFAULT 1 CHECK (version > 0);
