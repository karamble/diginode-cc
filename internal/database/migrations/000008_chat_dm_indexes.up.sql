CREATE INDEX IF NOT EXISTS idx_chat_messages_to_node ON chat_messages(to_node);
CREATE INDEX IF NOT EXISTS idx_chat_messages_from_node ON chat_messages(from_node);
