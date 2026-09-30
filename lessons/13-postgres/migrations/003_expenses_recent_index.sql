CREATE INDEX expenses_recent_idx
    ON expenses (created_at DESC, id DESC);
