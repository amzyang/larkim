-- Feishu re-serialises an edited text body as rich text, one <p> per line, and
-- a merged-forward bundle carries that markup for each message it holds. Rows
-- rendered before storeRendered learned to flatten them kept the tags, and
-- rendered_at rewinds only when Feishu moves a message's update_time, so the
-- renderer never came back for them. Unlike the other re-render migrations this
-- one spends a lark-cli round trip: the flattening happens to the renderer's
-- answer, so the text has to be fetched again.
UPDATE messages SET rendered_at = 0
WHERE msg_type IN ('text', 'merge_forward') AND rendered_at <> 0 AND content LIKE '%<p>%';
