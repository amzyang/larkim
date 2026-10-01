# AI panel — TECH

Implements [PRD.md](PRD.md); behavior lives there. Pinned to 451a84374df4bb33ffcdff9746430a61b9e5a866.

## Context

Today `:ai <form>` runs one throwaway question: [`startAI` (tui/app.go:2797)](https://github.com/amzyang/larkim/blob/451a84374df4bb33ffcdff9746430a61b9e5a866/tui/app.go#L2797)
builds `ai.Transcript` from the loaded page, closes the whole right stack, and streams into pane singletons
([app.go:297-309](https://github.com/amzyang/larkim/blob/451a84374df4bb33ffcdff9746430a61b9e5a866/tui/app.go#L297-L309))
drawn by [`renderAI` (tui/view.go:998)](https://github.com/amzyang/larkim/blob/451a84374df4bb33ffcdff9746430a61b9e5a866/tui/view.go#L998);
[`ai.Client` (ai/ai.go:49)](https://github.com/amzyang/larkim/blob/451a84374df4bb33ffcdff9746430a61b9e5a866/ai/ai.go#L49)
starts one agent process per question and refuses every permission. Pieces this builds on: the two composer sides
([tui/sidebox.go](https://github.com/amzyang/larkim/blob/451a84374df4bb33ffcdff9746430a61b9e5a866/tui/sidebox.go)),
`clickZone` ([tui/rows.go:54-157](https://github.com/amzyang/larkim/blob/451a84374df4bb33ffcdff9746430a61b9e5a866/tui/rows.go#L54-L157)),
the fill precedents [`fillReEdit` (tui/reedit.go:71)](https://github.com/amzyang/larkim/blob/451a84374df4bb33ffcdff9746430a61b9e5a866/tui/reedit.go#L71)
and [`chooseCandidate` (tui/candidates.go:68)](https://github.com/amzyang/larkim/blob/451a84374df4bb33ffcdff9746430a61b9e5a866/tui/candidates.go#L68),
`agentctx.Render` for the context window, the TUI-owned-tables pattern of `0022_drafts`/`0037_draft_frames`, and
docs/stream-card route A. Next free migration number: **0046** (0045 is `silence_settle_queue`).

### Measured on 2026-10-01 (omp 18.4.6, acp-go-sdk v0.13.5, throwaway cwd, self p2p)

1. `--tools` and `--no-tools` are honoured under `--mode acp`. `--tools <bogus>` rejects with the built-in list:
   `read, bash, edit, ast_grep, ast_edit, ask, debug, ida, eval, github, glob, grep, find, lsp, checkpoint, rewind,
   context_notes, new_context, security_scan, task, wait, todo, web_search, write, memory_edit, retain, recall,
   reflect, learn, manage_skill`. A prompt under `--no-tools` produced 0 tool calls; under `--tools bash` exactly 1
   (the shell). So the hardened configurations are real, not hoped for.
2. **RequestPermission is not guaranteed.** With default settings the shell ran with no permission request at all —
   omp's own per-tool settings allow it silently. The `session/update` `tool_call` did arrive before the result was
   used: `title "$ printf larkim-probe-ok"`, `kind execute`, `rawInput {"command": "printf …", "timeout": 30}`.
   Enforcement therefore keys off the tool_call update, never off permissions. The raw command is parseable.
3. A user-identity **reply** with `msg_type: interactive` accepts `PATCH` rewrites (patched twice, then both probe
   messages recalled), so Stream to chat can quote-reply and still stream.
4. ctrl+s reaches a pane through kitty + herdr (`cat -v` printed `^S`).

## Proposed changes

Seven phases; each merges alone. P4 and P6 need P3, everything else needs only P1.

### P1 — overlay, sessions in memory, follow-ups (~10 files)

- The AI column stays a root view like the info pane. Making it a `rightKind` would turn
  [`threadOpen()` (tui/right.go:80)](https://github.com/amzyang/larkim/blob/451a84374df4bb33ffcdff9746430a61b9e5a866/tui/right.go#L80)
  true everywhere and re-arm `rightDraftPin` on pop.
- The AI input is a third composer side, `sideAI`, not a new mode: INSERT, paste, ctrl+v, ctrl+g and `composerRows`
  come for free. Touch `area`/`areap`, `pickSide`, `holdSide`, `bandAt`, `bandWidth`/`bandLeft`, `cmdSide`,
  `splitBand` (sidebox.go), `renderBand`, `cursorAt`, `layout` (view.go), `composerAbove` (replybar.go:159).
  `submit` (app.go:2547) asks on `sideAI`; `replan` skips `planDraft` there.
- `View`/`rightColumn` (view.go:801, 1164) check `aiOpen` before `rightHasComposer`. `openRightIn` (right.go:120),
  Esc, `focusMessages` and `toggleInfo` hide instead of stopping; `closeAI` keeps focus when a frame sits under;
  `enterChat` re-points at the new chat's latest session.
- Per-session stream state replaces the singletons, keyed by turn id (`aiChunkMsg`, `waitForAI`, data.go:222/447);
  "unknown turn" replaces the `aiGen` check. New handlers take `cmd :=` out before returning (CLAUDE.md rule).
- Package ai: a pure function composes the turn text — system prompt (rewritten for `<reply>`), the window in
  agentctx form without paths or `More`, an optional `ImgText` on `agentctx.Input` (leaving `Y` output unchanged),
  chips, the last 10 Q/A pairs, the question. Gathers off the Update loop like tui/copy.go:64-156. A `ToolCall`
  update ends the turn (`stopped: agent used <title>`); `StopReasonCancelled` is not an error.
- `a`/`A` (app.go:2032), VISUAL `a` (app.go:2079), `:ai` (command.go:68), help rows (help.go:127-132), answers via
  `mdRows` (tui/markdown.go:23), Unread keyed by `feedChatAt` (unreadpanel.go:107). The `aiDraft` hand-off survives
  until P3 but only fires into the chat its question came from.

### P2 — persistence (~5 files)

- Migration `0046_ai_sessions.sql`: `ai_sessions` + `ai_turns`, TUI-owned, no `data_rev` triggers (the `drafts`
  pattern). Ids are uuids so a stream key exists before its insert lands; turn rows are upserted whole so
  out-of-order writes can't lose state; a row still streaming at load is "interrupted".
- store/ai.go, a SCHEMA.md section, an entry in CLAUDE.md's TUI-owned list. `D` goes through y/n (recall.go:63).

### P3 — cards and local actions (~6 files)

- `<reply>` parsing is pure (zero blocks, several, unclosed while streaming, inside a code fence). A card draws as
  Send would post it (`planDraft` + `bodyRows`, view.go:355-368).
- `clickZone` gains an action field (rows.go:93) that `live()` (rows.go:128) and `pressZone` (app.go:3007) read.
- Insert generalizes `fillReEdit`/`chooseCandidate`; the frame's box is used only when the anchor's thread is the
  frame under the panel (opening a frame to fill it races `takeRightDraft`, right.go:218).
- Copy is a `yankSources` branch (app.go:2147) because `yy` is the y prefix. Regenerate and Retry re-ask from the
  recorded context with the window cut at the original ask time.

### P4 — Send and Reply (~4 files)

- Extract `sendText(dest, text)` from `submit` (app.go:2547-2601) and share it with the composer.
- y/n through `confirmation`/`answerConfirm` (recall.go:74-90) carrying a payload like `reEdit` (app.go:129).
- Local-file uploads and `@name` resolution follow PRD "Actions"; `tagMentions` runs only for the open chat.

### P5 — snippets (~5 files)

- `config.AI.Snippets` (list, read-only in `:config`, settings.go:36) over built-ins in package ai; chip row,
  `1`-`9`, and a `/` pum branch that fires only at the start of a `sideAI` value (pum.go:91/121/164).

### P6 — Stream to chat (~6 files)

- larkcli: send inline interactive (send and reply variants) and rewrite message content
  (`PATCH /open-apis/im/v1/messages/{id}`), with `larkcli.Fake` support. Reply+PATCH is verified (Context #3).
- One writer per message, latest text wins, own lane of width 1 (per-message serial is required: no `sequence`, the
  API rate-limits ~2 writes/s per message). Card-dialect check before writing (docs/markdown-lint/DIALECT.md).
- `ai_turns` keeps the posted message id; Recall reuses `recallCmd` (recall.go:17).

### P7 — History tools (~3 files)

- No MCP server. With `ai.history` on, the system prompt teaches the exact `larkim --config … messages list
  --chat … --before/--around/--limit --json` and search invocations; the agent runs them with its shell tool.
- Enforcement (Context #2): on each `tool_call` update, parse `rawInput.command`; pass only an allowlisted
  read-only larkim invocation whose `--chat` is the session's chat, cancel the turn on anything else. Permissions
  are refused as today, since they may never arrive. Each running call leaves a trace line.
- Recommended `ai.agent` values go in README and config.example.yaml: `--no-tools` normally, `--tools bash` with
  `ai.history`. larkim never rewrites the configured argv; the tool-call gate holds regardless of argv.

## Testing and validation

`just vet` + `go test -race ./...` per phase. Tests are in-package, testify, the fake AI streamer
(tui/ai_test.go:58), `store.Open(t.TempDir())`, `larkcli.Fake` for every write, `clickAt` for zones.

- P1: open-without-asking (0 `Stream` calls), anchor rules per pane, Esc restores the frame and its draft, hiding
  keeps a stream alive and re-points on chat switch, no AI-column key reaches the hidden frame's `selected()`, the
  composed turn text (golden test), tool-call stops the turn, paste row in `TestForward_EveryInputTakesAPaste`.
- P2: upsert ordering, latest-per-chat, delete cascade, interrupted-at-load, `D` asks first.
- P3: the four parser cases; zone edges hit and neighbours miss; Insert box/quote/fold matrix; Replace; Regenerate
  window cut.
- P4: the PRD Send/Reply acceptance items against `larkcli.Fake`, including the upload choice and @all wording.
- P5: snippet insert paths never ask; `ai.snippets` replaces built-ins.
- P6: writer coalescing and final-write ordering (fake clock), `(interrupted)`, failed-send fallback.
- P7: gate accepts only the allowlisted shapes; every other command cancels.
- Manual, in kitty, writes only to CLAUDE.local.md chats: the PRD Acceptance walk (ask/follow/restart, Esc and chat
  switch mid-answer, Insert into chat and thread boxes, Send/Reply in a group, thread reply in a topics group,
  Stream to chat in self then robot p2p including mid-stream stop, History tools past the window).

## Parallelization

P1 → P2 → P3 is one deep chain through the same files (app.go, sidebox.go, view.go) and belongs to a single
implementer; sub-agents there would collide on every edit. From P3 on the fan-out is real and worth taking:

- `p4-send` — P4, local worktree `../larkim-p4`, branch `ai/p4-send-reply` (app.go, recall.go, submit helper).
- `p5-snippets` — P5, worktree `../larkim-p5`, branch `ai/p5-snippets` (pum.go, config, ai built-ins).
- `p7-history` — P7, worktree `../larkim-p7`, branch `ai/p7-history` (ai/ai.go gate + prompt, config, README).

Each merges one PR back onto the P3 baseline in that order; P6 follows P4 serially (it reuses `sendText`'s send
half and the confirm payload). `go test ./...` is the merge gate each PR owns for its files.

## Risks

- The tool-call gate turns a model that reaches for `read` despite instructions into a visibly stopped answer
  (today it would silently succeed). Mitigation: system prompt + recommended `--no-tools`; Retry is one key.
- `composerRows` is a shared split; the AI band's snippet row must keep every box level or the folded layout
  shifts. Covered by a dedicated height test in P1.
- Card writes are unsequenced; the serial writer is correctness, not an optimization. The 30 KB cap ends a card
  with a notice.
- Migration is append-only; reverting P2 leaves two unused tables, which is the designed rollback.
