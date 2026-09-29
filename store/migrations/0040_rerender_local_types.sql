-- Forwarded bundles and the small card types are rendered in process now,
-- from bodies and children larkim already holds, so their stored renderings
-- are redone to whatever this build spells them as. This costs no lark-cli
-- round trip: every type listed here renders locally. A bundle whose
-- expansion has not landed simply waits behind the render queue's guard on
-- forwarded_roots.fetched_at.
UPDATE messages SET rendered_at = 0
 WHERE deleted = 0
   AND msg_type IN ('merge_forward', 'share_chat', 'share_user', 'location',
                    'folder', 'vote', 'hongbao', 'todo');
