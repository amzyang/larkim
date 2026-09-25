-- mentions_json and reactions_json were stored exactly as lark-cli printed
-- them, indentation included, while namesSelf looks for `"id":"<id>"` as text.
-- No stored mention ever matched, so the chat list's @ marker and the mentions
-- panel were both permanently empty. The store now compacts these columns on
-- write; this brings the rows written before that in line.
-- json_valid guards each one: the store keeps a body it cannot parse rather
-- than dropping it, so a row that does not parse is a state this has to
-- survive rather than abort on.
UPDATE messages SET mentions_json = json(mentions_json) WHERE json_valid(mentions_json);
UPDATE messages SET reactions_json = json(reactions_json) WHERE json_valid(reactions_json);
UPDATE chats SET last_mentions_json = json(last_mentions_json) WHERE json_valid(last_mentions_json);
UPDATE chats SET last_reactions_json = json(last_reactions_json) WHERE json_valid(last_reactions_json);
