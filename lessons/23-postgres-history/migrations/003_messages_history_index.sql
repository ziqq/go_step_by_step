CREATE INDEX lesson23_messages_history_idx
    ON lesson23_messages (conversation_id, created_at DESC, id DESC);
