CREATE TABLE lesson23_memberships (
    conversation_id text NOT NULL,
    user_id         text NOT NULL,
    active          boolean NOT NULL DEFAULT true,
    PRIMARY KEY (conversation_id, user_id)
);
