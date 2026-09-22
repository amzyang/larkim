# larkim

Feishu/Lark IM synced to local SQLite, with a CLI and (soon) a TUI on top. Runs as a background service, keeps your chats, messages, contacts and attachments in `~/.larkim/larkim.db`, and answers queries without touching the network.

larkim drives the official [lark-cli](https://github.com/larksuite/cli) for authentication and every API call; it never stores credentials itself.

## Install

```sh
brew install amzyang/tap/larkim
npm install -g @larksuite/cli
lark-cli auth login --domain im,contact
brew services start larkim
```

## Use

```sh
larkim status                                  # sync state, counts, recent ticks
larkim chats list --search 兜底
larkim messages list --chat "FDEV兜底" --since 24h --limit 20
larkim messages list --sender ou_xxx --type image --json
larkim messages show om_xxx
larkim messages thread om_xxx
larkim sync --backfill-days 7                  # one tick in the foreground (daemon must be stopped)
larkim db path && larkim schema                # for direct SQLite consumers, see docs/SCHEMA.md
```

Output is a table on a terminal and JSON when piped or with `--json`.

## How it syncs

Every tick (default 10s) a cross-chat message search over a sliding window discovers new message ids; unknown ids are fetched in batches of 50 with millisecond timestamps and raw bodies, then rendered to readable text through lark-cli. Every 10 minutes the full chat list is refreshed and the 30 most active chats are reconciled from their cursors, which catches anything the search index misses. History is backfilled a few chats per tick (default 30 days). Edits and recalls update in place; recalled messages keep their last known body.

When the user token expires the daemon stops calling the API, reports `needs_login` in `larkim status`, and resumes after `lark-cli auth login`.

## Configuration

`~/.larkim/config.yaml` (all optional):

```yaml
data_dir: ~/.larkim
lark_cli_path: /opt/homebrew/bin/lark-cli
poll_interval: 10s      # minimum 1s
overlap: 2m             # search-index latency allowance
backfill_days: 30
active_top_k: 30
resources:
  max_bytes: 52428800
```

## Library

`store`, `sync`, `larkcli` and `config` are importable Go packages. A consumer that acquires `sync.TryLock` may run `sync.Syncer.Run` in-process; otherwise it reads the store while the daemon writes.

## Development

```sh
make test
make build && ./larkim --config ./dev.yaml sync
```
