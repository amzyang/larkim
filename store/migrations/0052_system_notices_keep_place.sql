-- The client does not lift a chat for a system notice — a member joining, a
-- rename, a call ending — so the sort key now skips them unless a chat holds
-- nothing else. This recomputes the key the way chatSummarySortKey does.
UPDATE chats SET last_unsilenced_ms = CASE
 WHEN EXISTS (SELECT 1 FROM messages m WHERE m.chat_id = chats.chat_id AND m.message_position >= 0 AND m.msg_type <> 'system')
 THEN (SELECT COALESCE(max(m.create_ms), 0) FROM messages m
       WHERE m.chat_id = chats.chat_id AND m.message_position >= 0 AND m.silenced = 0 AND m.msg_type <> 'system')
 ELSE (SELECT COALESCE(max(m.create_ms), 0) FROM messages m
       WHERE m.chat_id = chats.chat_id AND m.message_position >= 0 AND m.silenced = 0)
 END;
