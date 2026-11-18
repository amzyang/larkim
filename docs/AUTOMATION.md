# Message automation

Automation matches synchronized messages against configured rules and runs an external executable for each match. The process holding `daemon.lock` owns evaluation and dispatch: either `larkim daemon run` or a TUI with embedded sync. A TUI attached to an existing daemon does not run another dispatcher. One-shot `larkim sync` records message changes but leaves them queued for the next owner.

Rules are read at startup. Editing `automation` requires restarting the lock owner; live configuration reload does not change running rules. No rules are enabled by default.

## TUI configuration

Open `:config automation` or select the Automation tab in `:config`.

| Key | Action |
| --- | --- |
| `a` | Create a disabled custom rule. |
| `q` | Create a disabled quota preset using the current larkim executable. |
| `Enter` | Edit the selected rule; an existing rule ID is immutable. |
| `Space` | Toggle the configured enabled state. |
| `d`, then `y` | Delete the rule from configuration, retaining execution history. |
| `c`, then `y` | Check a quota preset without submitting a claim. |

In the editor, Tab/Shift+Tab move between fields; Enter opens Chat, Sender or message-type selection. Chat and Sender pickers support name/ID search and include known bots. Lists and callback arguments are JSON arrays of strings; arguments are not shell-parsed. Ctrl+S saves, and Esc discards the draft. The quota preset derives its callback arguments from its single Chat and Sender.

The list displays **configured**, not runtime, status. A saved change is marked **Restart required** for the daemon or embedded-sync TUI. Saving or deleting a rule does not change the running worker. Runtime job history remains available through `larkim automation jobs`.

Check is available only for the current binary's quota preset. It requires confirmation because it may foreground Lingxi. It selects a current eligible card from the latest 100 locally synced interactive messages in the configured Chat/Sender scope; it never runs the configured executable and never clicks the claim button. A missing/expired card or unavailable AX window is an error. Checking does not enqueue or consume a job.

## Configuration

```yaml
automation:
  - id: build-ready
    enabled: false
    criteria:
      chat_ids: [oc_quiet]
      sender_ids: [ou_a]
      message_types: [text, interactive]
      contains: [build, ready]
      excludes: [cancelled, dry run]
    callback:
      executable: /usr/bin/env
      args: [/Users/linlan/bin/build-ready-callback]
      timeout: 30s
```

The callback in this example is a user-supplied executable. The built-in Lingxi quota callback is described below. Keep a rule disabled until its executable, scope, and side effects have been verified.

Each rule has a unique, nonempty `id`. Its callback must specify an executable and a positive timeout. At least one criterion is required.

Criteria combine as follows:

- Different populated fields are **AND** conditions.
- `chat_ids`, `sender_ids`, and `message_types` each match **any** listed value.
- `contains` requires **all** listed substrings in the normalized message text.
- `excludes` rejects the message if **any** listed substring occurs.
- An omitted or empty list places no restriction on that field.

A new activation starts from now, using message creation time as its cutoff; historical messages are not replayed. Restarting with the same enabled rule preserves its activation cutoff. Re-enabling a rule or changing its criteria or callback starts a new activation and cancels pending jobs from the old configuration. Disabling or removing a rule cancels its pending jobs. These changes take effect on owner startup.

Message edits and rendering updates are evaluated against current content. Jobs are durable and deduplicated by rule and message: repeated synchronization or later edits do not create another execution of a job already recorded for that pair. A message recalled before dispatch is not executed. A callback that has already started may have caused an external effect; recalling its source message cannot undo that effect.

## Callback protocol

The executable receives its configured `args` directly, without shell parsing. Shell quoting, pipes, variable expansion, and redirection are not interpreted. The callback reads one JSON event from standard input:

```json
{
  "version": 1,
  "job_id": 42,
  "rule_id": "build-ready",
  "message": {
    "message_id": "om_build_ready",
    "chat_id": "oc_quiet",
    "sender_id": "ou_a",
    "msg_type": "text",
    "create_ms": 1800000000000
  },
  "text": "build ready"
}
```

`message` is the synchronized message record, including its public fields; the example shows a subset. `text` is the normalized current text used for matching. `job_id` is the stable persisted job identifier. Callbacks can use it as an idempotency key in the external system they control. The stored job retains its enqueue-time payload and callback configuration. Dispatch rechecks the current message and passes that current message and text to the callback.

On a normal exit, standard output must contain exactly one JSON result object:

```json
{"status":"succeeded","result":"Build notification delivered"}
```

The status is one of:

| Status | Meaning |
| --- | --- |
| `succeeded` | The callback verified that its intended effect completed. |
| `failed` | The callback certifies that no external effect occurred. |
| `unknown` | The callback cannot determine whether an external effect occurred. |

`result` is a string containing the outcome detail. Diagnostics belong on standard error, not standard output; they are retained with the job's diagnostic result. A nonzero exit, invalid output, timeout, or interrupted execution is `unknown`, not `failed`. In particular, reporting an error does not prove that an earlier click or API request had no effect.

Only one callback runs at a time. Evaluation continues independently while a callback is running. Shutdown cancels the callback and waits for dispatch to finish before releasing `daemon.lock`. At startup, jobs left `running` by a stopped process become `unknown`; this reconciliation also runs when the configured rule list is empty.

There are no automatic retries. Investigate an `unknown` outcome in the external system before taking another action. The engine does not provide exactly-once external effects or replay historical matches.

## Inspect jobs

```sh
larkim automation jobs
larkim automation jobs --limit 20
larkim automation jobs --json
```

`--limit` must be positive and defaults to 50. Jobs are listed newest first. Text output includes `job_id`, `rule_id`, `message_id`, `status`, and `created`. JSON includes the complete persisted job, including its payload, callback snapshot, result, and timestamps. Queue states are `pending`, `running`, and `cancelled`; terminal execution outcomes are `succeeded`, `failed`, and `unknown`.

The persistent tables and column contracts are documented in [SCHEMA.md](SCHEMA.md).

## Lingxi quota callback

`larkim automation claim-quota` consumes the callback event on stdin and emits the same outcome protocol. It supports the Chinese repeat-claim card with the exact button label `再次申请加油包（+50%）`. First-time claims, changed card formats, and overnight periods are refused.

```yaml
automation:
  - id: quota-refill
    enabled: false
    criteria:
      chat_ids: [oc_quiet]
      sender_ids: [ou_a]
      message_types: [interactive]
      contains: ["AI Coding 总额度已用", "再次申请加油包（+50%）"]
    callback:
      executable: /Users/linlan/bin/larkim
      args: [automation, claim-quota, --chat-id, oc_quiet, --sender-id, ou_a]
      timeout: 60s
```

Use the actual installed binary path and explicitly authorized chat/sender IDs in local configuration. The example is disabled and contains fictional IDs. Configuring a rule does not grant authorization for other chats or actions.

Requirements: `cua-driver` on PATH, its local daemon running with normal approvals, Accessibility permission, a logged-in Lingxi client, and an unlocked desktop. No token or new language runtime is required. The callback uses `applink.Open` to open the native `lark://` message-position link in the registered Lingxi client. It waits up to ten seconds for a unique Lingxi window with a readable chat AX surface; navigation is submitted once, and ambiguous windows are refused. This changes the visible conversation and may mark it read; foreground-free execution is not promised.

The callback validates the event's chat, sender, live interactive type, message ID and positive position before desktop access. It uses the message creation date and the card's UTC+8 time range, then checks expiry again immediately before clicking. Only one card may match its account, time range, total/per-strategy amounts and prior refill count. It uses an AX element token, never coordinates, and refuses incomplete/degraded snapshots or multiple matching cards/buttons.

AX does not expose a proven message-ID-to-card binding. Matching uses card content after position navigation; it cannot distinguish cards with identical content that are outside the loaded AX tree. The current card layout must be validated on the installed client before enabling unattended claims. Concurrent navigation by a person or another automation process can invalidate the target; no click is retried.

After the single click request, the callback observes for at most ten seconds. Success requires one card with the same account and time range, the next refill count, and the expected increased total, Claude and GPT limits. A transport error after submission or an unverified result is `unknown`. The callback itself has a 45-second deadline; the enclosing callback timeout should be longer.

For non-submitting validation, pass a current event file through stdin:

```sh
larkim automation claim-quota --chat-id oc_quiet --sender-id ou_a --check < event.json
```

`--check` may open and foreground Lingxi but never submits a claim. A successful check returns `status: failed` with `check passed` in `result`: no quota effect occurred, so it must not be recorded as a successful claim. Do not put `--check` in an enabled automation rule, because its job would become terminal without claiming.
