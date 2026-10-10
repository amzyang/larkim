-- jev_json keeps Jev's whole answer beside the verdict it led to, so a cut —
-- attention, asking, addressed — can be re-read off stored answers rather than
-- by asking again. Compact JSON {"model","pick","fits","nouls"}; NULL when a
-- rule decided or the call failed.

ALTER TABLE triage ADD COLUMN jev_json TEXT;
