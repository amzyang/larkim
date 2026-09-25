# Silence — 技术方案

行为见 [PRD.md](PRD.md)。

## 结构

静音是 `messages` 上的一个派生列，求值挂在仅有的两条写消息的路径上，与 `refreshChatSummary` 共用事务：

```
UpsertMessages (store/messages.go:73)   → applySilence(本批 ids) → refreshChatSummary
UpdateRendered (store/messages.go:138)  → applySilence(该 id)    → refreshChatSummary
Syncer.tick    (sync/syncer.go:189) 头部 → ReapplySilence         → 指纹不一致则全量重判
```

第二个求值点的理由是渲染异步：`content` 由 lark-cli 补齐，卡片与富文本在 ingest 那一刻只有 body JSON，内容规则判不准。ingest 时按当时可见的内容判一次，渲染到达再判一次并覆盖。覆盖是双向的——不再命中就取消静音，编辑与撤回走同一条路径。

`UpdateRendered` 原本直接按 `last_message_id` 改 `chats.last_*`；静音状态可能恰在这一刻翻转，因此改为重算该会话的摘要，否则刚被静音的消息会留在排序键里。

## 规则只有一份 SQL

`store/silence.go`：

```go
type SilenceRule struct{ Chat, Sender, Contains string }
type SilenceRules []SilenceRule

func (r SilenceRule) where() (string, []any) // m.chat_id = ? AND m.sender_id = ? AND instr(…) > 0
func (rs SilenceRules) Fingerprint() string  // 规范化后的 sha256
```

打标、全量重判、诊断命令共用 `where()`，语义没有第二处实现可漂移。内容匹配的取值是 `lower(CASE WHEN m.content <> '' THEN m.content ELSE m.content_raw END)`，未渲染时退到 body JSON。

打标本身也不写 Go matcher：

```sql
UPDATE messages SET silenced = 0 WHERE message_id IN (…);
UPDATE messages SET silenced = 1 WHERE message_id IN (…) AND <rule>;  -- 每条规则一句
```

先清零再逐条置位，所以删规则、改规则、消息被编辑都收敛到正确值。规则集为空时只剩清零那一句。

## 数据

`store/migrations/0014_silence.sql`：

```sql
ALTER TABLE messages ADD COLUMN silenced INTEGER NOT NULL DEFAULT 0;
ALTER TABLE chats ADD COLUMN last_unsilenced_ms INTEGER NOT NULL DEFAULT 0;
UPDATE chats SET last_unsilenced_ms = last_message_ms;
```

回填让没有规则的库与迁移前行为一致。两列都是派生物，指纹一变就整体重建。

`refreshChatSummary`（store/chats.go:311）在摘要之外多算一个排序键：

```sql
SELECT COALESCE(max(create_ms), 0) FROM messages
 WHERE chat_id = ? AND message_position >= 0 AND silenced = 0
```

thread 口径与既有摘要一致（负 position 不参与）；撤回的消息不排除，与「撤回后会话位置不变」同一条规则。

`docs/SCHEMA.md` 随列一并更新：`messages.silenced`、`chats.last_unsilenced_ms`、会话排序口径、徽标口径。

## 排序与计数

| 位置 | 改法 |
|---|---|
| `ListChats` ORDER BY（store/chats.go:257） | `(u.chat_id IS NOT NULL) DESC, c.last_unsilenced_ms DESC, c.last_message_ms DESC, c.name` |
| `unreadJoin`（store/chats.go:230） | 谓词换成 `unreadCounted` |
| `UnreadCountsByChat`（store/resources.go:210） | 谓词换成 `unreadCounted` |
| `MarkChatRead`（store/resources.go:191） | 保持 `unreadInPane`，不加 `silenced = 0` |

```go
const unreadInPane = `r.is_read_remote = 0 AND r.local_read_at = 0 AND m.deleted = 0`
const unreadBadge = unreadInPane + ` AND m.message_position >= 0`
const unreadCounted = unreadBadge + ` AND m.silenced = 0`
```

`MarkChatRead` 不加 `silenced = 0` 是硬约束：`tui/badgeclear.go` 的 `unreadWaiting` 逐项复刻 `unreadBadge` 决定投不投 applink，而 `MarkChatRead` 必须能收掉它看见的每一条，否则每次 reload 都重投（见 [read-sync/TECH.md](../read-sync/TECH.md) 的「门控」）。本地已读的集合保持为徽标集合的超集，这条不变式就成立，顺带静音消息也不会永远挂在 read-status 轮询里。

`last_unsilenced_ms` 为 0 的会话——全部消息被静音的，和一条消息都没有的——沉到底部，组内按 `last_message_ms` 排。

## 注入与生效范围

`Store` 加导出字段 `Silence SilenceRules`，由 `cli.App.openStore`（cli/root.go:92）从 `cfg.Silence` 填；`store.Open` 的签名不变，测试照常裸开库。只有持 `daemon.lock` 的进程写 `messages`，规则因此只在写者侧起作用；连着 daemon 的只读 TUI 与 CLI 读的是 `silenced` 列，不需要规则。

`Syncer.tick` 头部调 `Store.ReapplySilence`：读 `sync_state.silence_rev` 与 `Fingerprint()` 比对，不一致则在一个事务里清零、逐规则置位、重算所有会话摘要、写回指纹。代价是每 tick 一次 keyed SELECT，换来规则变更自愈，不依赖任何一次性的启动钩子。

## 配置

`config.Config` 加 `Silence` 字段（yaml key `silence`），`Load` 拒绝三个字段全空的规则——那条规则会静音一切。`config.example.yaml` 给出示例。

## 诊断

`larkim silence`（cli/silence_cmd.go）按配置顺序列出规则，每条给命中数与最近一次命中时间：

```sql
SELECT count(*), COALESCE(max(create_ms), 0) FROM messages m WHERE <rule>
```

静音的失败模式是静默的：一个写错的 id 只表现为「没生效」，命中数为 0 就是答案。

## 测试

store 层白盒，真 SQLite：

| 用例 | 断言 |
|---|---|
| `TestSilenceRules_RejectARuleThatMatchesEverything` | 空规则被拒 |
| `TestSilenceRules_FingerprintFollowsTheRules` | 指纹随规则变化，且对相同规则稳定 |
| `TestUpsertMessages_SilencesAMatchingSender` | ingest 即打标 |
| `TestUpsertMessages_LeavesAnUnmatchedFieldAlone` | 字段之间是 AND |
| `TestUpsertMessages_SilencesOnTheRawBodyBeforeRendering` | 内容规则不等渲染队列 |
| `TestUpdateRendered_SilencesOnTheRenderedBody` | 只有渲染后才命中的内容规则 |
| `TestUpdateRendered_ClearsSilenceWhenTheRenderingStopsMatching` | 覆盖双向 |
| `TestUpdateRendered_RefreshesTheSummaryWhenSilenceFlips` | 排序键不留旧值 |
| `TestReapplySilence_RewritesEveryMessageWhenTheRulesChange` | 指纹驱动全量重判 |
| `TestReapplySilence_WritesNothingWhenTheFingerprintMatches` | `data_rev` 不动，TUI 不空转 |
| `TestRefreshChatSummary_KeepsTheNewestMessageAndSinksTheSortKey` | 摘要取最新、排序取最新非静音 |
| `TestUnreadCountsByChat_LeavesOutSilencedMessages` | 计数口径 |
| `TestListChats_DoesNotLiftAChatWhoseUnreadIsAllSilenced` | 未读组 |
| `TestListChats_SinksAFullySilencedChat` | 沉底但保留摘要 |
| `TestMarkChatRead_MarksSilencedMessagesToo` | 超集不变式 |
| `TestSilenceMatches_CountsOneRule` | 诊断命令的数据源，含命中为 0 的规则 |

sync：`TestTick_RebuildsSilenceWhenTheRulesChange`（改配置后一次 tick 即落到历史消息与排序键）、
`TestTick_LeavesSilenceAloneWhenTheRulesHold`（指纹一致的 tick 不写 `data_rev`）。
config：`TestLoad_ParsesSilenceRules`、`TestLoad_RejectsASilenceRuleThatMatchesEverything`。
cli：`TestSilenceCmd_ReportsPerRuleMatches`、`TestSilenceCmd_RejectsARuleThatMatchesEverything`。

数据一律虚构（`oc_quiet`、`cli_c`、`ou_a`）。
