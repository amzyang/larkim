-- data_rev answers "has what I display changed", not "has anyone written".
--
-- Every sync round stamps bookkeeping on rows it did not otherwise touch: a
-- chat listing rewrites last_seen_at for all of them, a re-ingested message
-- gets a new last_seen_at, the mute rotation stamps mute_checked_at, the
-- backfill advances cursor_ms. Under a blanket AFTER UPDATE each of those
-- told every reader to re-read a list that had not changed, once per row.
--
-- The guard is a WHEN clause rather than AFTER UPDATE OF: UPDATE OF fires on
-- any statement that names the column, and the listing upsert names every
-- display column on every round whether or not the values moved. Comparing
-- NEW to OLD asks the question that matters.
--
-- Columns left out are the ones that exist to pace the syncer and that
-- nothing renders. Anything else counts as a change, so a column added later
-- costs a reload it may not need rather than silently going unnoticed.

DROP TRIGGER chats_rev_au;
CREATE TRIGGER chats_rev_au AFTER UPDATE ON chats
WHEN (NEW.name, NEW.description, NEW.chat_mode, NEW.chat_status, NEW.owner_id, NEW.external,
      NEW.p2p_target_id, NEW.p2p_target_type, NEW.avatar_url, NEW.avatar_path, NEW.left_at, NEW.sync_error,
      NEW.last_message_id, NEW.last_message_ms, NEW.last_sender_id, NEW.last_sender_name, NEW.last_sender_type,
      NEW.last_msg_type, NEW.last_content, NEW.last_content_raw, NEW.last_mentions_json, NEW.last_reactions_json,
      NEW.last_rendered_at, NEW.last_deleted, NEW.last_unsilenced_ms, NEW.muted)
 IS NOT (OLD.name, OLD.description, OLD.chat_mode, OLD.chat_status, OLD.owner_id, OLD.external,
      OLD.p2p_target_id, OLD.p2p_target_type, OLD.avatar_url, OLD.avatar_path, OLD.left_at, OLD.sync_error,
      OLD.last_message_id, OLD.last_message_ms, OLD.last_sender_id, OLD.last_sender_name, OLD.last_sender_type,
      OLD.last_msg_type, OLD.last_content, OLD.last_content_raw, OLD.last_mentions_json, OLD.last_reactions_json,
      OLD.last_rendered_at, OLD.last_deleted, OLD.last_unsilenced_ms, OLD.muted)
BEGIN
    UPDATE data_rev SET rev = rev + 1;
END;

DROP TRIGGER messages_rev_au;
CREATE TRIGGER messages_rev_au AFTER UPDATE ON messages
WHEN (NEW.chat_id, NEW.msg_type, NEW.sender_id, NEW.sender_type, NEW.sender_name,
      NEW.content_raw, NEW.content, NEW.create_ms, NEW.update_ms, NEW.message_position,
      NEW.updated, NEW.deleted, NEW.thread_id, NEW.reply_to, NEW.mentions_json, NEW.reactions_json,
      NEW.rendered_at, NEW.edited_at, NEW.silenced)
 IS NOT (OLD.chat_id, OLD.msg_type, OLD.sender_id, OLD.sender_type, OLD.sender_name,
      OLD.content_raw, OLD.content, OLD.create_ms, OLD.update_ms, OLD.message_position,
      OLD.updated, OLD.deleted, OLD.thread_id, OLD.reply_to, OLD.mentions_json, OLD.reactions_json,
      OLD.rendered_at, OLD.edited_at, OLD.silenced)
BEGIN
    UPDATE data_rev SET rev = rev + 1;
END;

DROP TRIGGER read_state_rev_au;
CREATE TRIGGER read_state_rev_au AFTER UPDATE ON read_state
WHEN (NEW.is_read_remote, NEW.consumed_at, NEW.local_read_at)
 IS NOT (OLD.is_read_remote, OLD.consumed_at, OLD.local_read_at)
BEGIN
    UPDATE data_rev SET rev = rev + 1;
END;

DROP TRIGGER resources_rev_au;
CREATE TRIGGER resources_rev_au AFTER UPDATE ON resources
WHEN (NEW.type, NEW.local_path, NEW.size_bytes, NEW.status, NEW.last_error)
 IS NOT (OLD.type, OLD.local_path, OLD.size_bytes, OLD.status, OLD.last_error)
BEGIN
    UPDATE data_rev SET rev = rev + 1;
END;

DROP TRIGGER contacts_rev_au;
CREATE TRIGGER contacts_rev_au AFTER UPDATE ON contacts
WHEN (NEW.name, NEW.email, NEW.is_bot, NEW.p2p_chat_id, NEW.avatar_url, NEW.avatar_path,
      NEW.enterprise_email, NEW.department, NEW.is_cross_tenant)
 IS NOT (OLD.name, OLD.email, OLD.is_bot, OLD.p2p_chat_id, OLD.avatar_url, OLD.avatar_path,
      OLD.enterprise_email, OLD.department, OLD.is_cross_tenant)
BEGIN
    UPDATE data_rev SET rev = rev + 1;
END;
