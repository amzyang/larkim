-- The message id of the card an answer streamed into the chat as. A turn
-- without one never reached the wire; the id outlives the stream, which is
-- the point: the card is a message in the chat now, and the panel's Jump and
-- Recall read it here after a restart.
ALTER TABLE ai_turns ADD COLUMN card_id TEXT NOT NULL DEFAULT '';
