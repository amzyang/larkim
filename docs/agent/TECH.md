# Agent context — 技术方案

行为见 [PRD.md](PRD.md)。代码引用锚定在 `b0d84f4`。

## Context

TUI 是单个 Bubble Tea `Model`，四个 pane、四个 mode，按键全部经 `onNormalKey` 的字符串 switch 分派：

- [`tui/app.go:41-102` @ b0d84f4](https://github.com/amzyang/larkim/blob/b0d84f481a9a18f2cdf39fa3b52ecd7684ae355e/tui/app.go#L41-L102) — `Model` 的全部状态；`msgs`/`msgIdx`、`thread`/`threadIdx`、`searching`/`searchResults` 是本次要读的选区来源。
- [`tui/app.go:465-575` @ b0d84f4](https://github.com/amzyang/larkim/blob/b0d84f481a9a18f2cdf39fa3b52ecd7684ae355e/tui/app.go#L465-L575) — `onNormalKey`；`y`/`Y` 当前直接 `tea.SetClipboard(id)`，同步返回。
- [`tui/app.go:711-767` @ b0d84f4](https://github.com/amzyang/larkim/blob/b0d84f481a9a18f2cdf39fa3b52ecd7684ae355e/tui/app.go#L711-L767) — `runCommand`，`:copy` 挂在这里。
- [`tui/view.go:172-208` @ b0d84f4](https://github.com/amzyang/larkim/blob/b0d84f481a9a18f2cdf39fa3b52ecd7684ae355e/tui/view.go#L172-L208) — `renderRows` 把消息摊成 `msgRow`，高亮只认 `r.idx == m.msgIdx`，选区要在这里扩成区间判定。
- [`tui/data.go:19-31` @ b0d84f4](https://github.com/amzyang/larkim/blob/b0d84f481a9a18f2cdf39fa3b52ecd7684ae355e/tui/data.go#L19-L31) — `Deps` 已有 `Self`（来自 `sync.KeySelfOpenID`，见 [`cli/tui_cmd.go:35-37`](https://github.com/amzyang/larkim/blob/b0d84f481a9a18f2cdf39fa3b52ecd7684ae355e/cli/tui_cmd.go#L35-L37)），但没有 `DataDir` 和 `ConfigPath`。
- [`ai/ai.go:93-116` @ b0d84f4](https://github.com/amzyang/larkim/blob/b0d84f481a9a18f2cdf39fa3b52ecd7684ae355e/ai/ai.go#L93-L116) — `ai.Transcript` 是「TUI 之外的纯渲染函数」的既有先例，本方案照搬这个形状。
- [`store/messages.go:169-232` @ b0d84f4](https://github.com/amzyang/larkim/blob/b0d84f481a9a18f2cdf39fa3b52ecd7684ae355e/store/messages.go#L169-L232) — `MessageQuery` 只有时间窗与 offset，没有游标；`limit <= 0` 会被兜底成 100。

两处与 PRD 表述不符的既有事实，方案按事实做：

1. **话题回复就混在 chat 消息流里。** 它们以所属 `chat_id` 入库、`message_position` 为负，`ListMessages` 不过滤，`renderRows` 也照显示——TUI 并没有「折叠」它们。所以排除是本次新增的规则，不是对现状的对齐。哨兵值由 API 决定：`docs/SCHEMA.md` 长期写作 `-1`，而线上数据全是 `-3`，`larkcli.Fake` 用的又是 `-1`——判据只能是符号。
2. **reactions 里没有 emoji 字符。** `reactions_json` 原样来自 lark-cli 的 `{counts, details}` 块，`counts[].reaction_type` 是 `THUMBSUP` 这类枚举名，仓库里从未解析过它。PRD 示例中的 `👍×3` 需要一张自维护的映射表才能得到。

## Proposed changes

### 新包 `agentctx`

纯渲染，不碰剪贴板、不碰 DB、不取时间与随机数（ARCH.md 一之「注入不确定来源」）：

```go
type Person struct{ Name, OpenID, Email string; Bot bool }

type Input struct {
    Now      time.Time
    Boundary string                        // 标签后缀，调用方注入
    Self     Person                        // 零值 = self_open_id 未知，省去 me 行
    Chat     store.Chat
    Members  int
    Contact  *Person                       // 仅 p2p
    People   []Person                      // 首次出现顺序
    Messages []store.Message               // 调用方已决定好的范围
    Threads  map[string]int                // thread_id → 回复数
    Res      map[string][]store.Resource   // message_id → 附件，LocalPath 已绝对化
    More     string                        // 尾部续查命令整行
}

func Render(in Input) string
func Boundary(r *rand.Rand, msgs []store.Message) string
func ParseRange(arg string) (Range, error)   // ":copy 200 | 7d | all"
```

`Boundary` 生成 4 位 hex 后重扫一遍所有正文，命中就重抽，保证「后缀不与正文冲突」这条验收可断言。`ParseRange` 落在本包而不是复用 `cli.parseTime`：`cli` 依赖 `tui`，反向不可行，且 `time.ParseDuration` 不认 `7d`（[`cli/root.go:117`](https://github.com/amzyang/larkim/blob/b0d84f481a9a18f2cdf39fa3b52ecd7684ae355e/cli/root.go#L117)）。

渲染细节：

- `Messages` 原样输出，不做过滤——话题回复的排除属于范围策略，留在调用方，`Render` 保持无策略。
- 正文不转义，附件单独成行接在正文之后（`[image <abs>]` / `[file <name> <size> (not downloaded)]`）。正文里 lark-cli 留下的内联标记（`![Image](img_xxx)`、`<file .../>`）保持原样，不做替换——替换要重新推断标记与 `file_key` 的对应关系，收益不抵风险。
- `reactions` 输出 `emoji_type` 原名（`reactions="THUMBSUP×3"`）。不引入 emoji 映射表：枚举名对 agent 一样可读，而映射表要随飞书表情库长期维护。**PRD 的 `👍×3` 示例需要同步改掉。** `counts` 的确切形状只有 lark-cli 的文档描述、仓库内无样本，解码失败时整个属性省略（`unverified`，实现时需取一条真实数据核对）。

### 数据装配（`tui/data.go`）

新增 `copyContext(d Deps, spec) tea.Cmd`，所有 DB 读在 Cmd 里做，回 `contextMsg{text string; n, bytes int; chat string}`，`Update` 收到后 `tea.SetClipboard(text)` 并 `notify`。当前 `y` 是同步的，加了联系人/附件/成员数查询后必须离开 Update 循环。

spec 由按键处理器同步算好，两类：

- **已在内存的选区**（messages/thread 的光标或 `v` 区间）→ 直接带 `[]store.Message`。
- **范围查询**（chats pane 的 `y`、`:copy`）→ 带 `store.MessageQuery`，Cmd 里查。chats pane 的 `y` = `SinceMs: now-24h, Desc: true, Limit: 10` 再反转；`:copy all` 必须传显式大 limit，否则被兜底成 100。

话题回复由 `MessageQuery.ExcludeThreadReplies` 在 SQL 里排除，只作用于范围查询——limit 必须数「留下来的」而不是「读到的」，否则重话题的群里 `:copy 200` 只剩个位数。显式选区不过滤。两类都在 Cmd 内做同一套补齐：收集 sender 与 mention 的 open_id、批量查联系人/附件/话题计数/成员数、把 `Resource.LocalPath` 按 `DataDir` 展开成绝对路径。

`Deps` 加两个字段，由 `cli/tui_cmd.go` 注入：`DataDir`（`a.cfg.DataDir`）、`ConfigPath`（`a.configPath`，空则 `config.DefaultPath()`）。续查命令的可执行文件名写字面量 `larkim`，不用 `os.Args[0]`（那可能是 `./larkim` 或 go test 的临时路径）。

### store 新增

都是只读查询，沿用 `queryAll` + `scanX` 的既有写法：

| 方法 | 用途 | 现状 |
| --- | --- | --- |
| `ContactsByIDs(ctx, ids) (map[string]Contact, error)` | people 区块的 email / bot 标记 | 只有单行 `GetContact` 与按名搜索的 `ListContacts` |
| `ResourcesForMessages(ctx, ids) (map[string][]Resource, error)` | 附件行 | `ResourcesFor` 是单条，范围复制会 N+1 |
| `ThreadReplyCounts(ctx, chatID, threadIDs) (map[string]int, error)` | 根消息的 `thread="N replies"` | 无。不从已载入的 `m.msgs` 数，那一页封顶 200 条，老根会算少 |

头部的 `<n>人` 用既有的 `ChatMemberCount`，不新增方法。

`MessageQuery` 加 `BeforeID` / `AfterID`。谓词用行值比较，与既有排序键严格对齐：

```sql
(m.create_ms, m.message_position, m.id) < (?, ?, ?)   -- BeforeID，> 为 AfterID
```

锚点三元组先用 `GetMessage` 取（`scanMessage` 已经带回 `ID` 与 `MessagePosition`）。`--around` 不进 SQL：`cli` 层发两次 `ListMessages`（`BeforeID` 取 N 条 + `AfterID` 取 N 条）再拼上锚点自身，比在一条语句里做 UNION 简单且可读。

### CLI

`messagesListCmd` 加 `--before` / `--after` / `--around` / `--context`（默认 20）。`--around` 与 `--before`/`--after` 用 `MarkFlagsMutuallyExclusive` 互斥，`--context` 不带 `--around` 时报错。`--offset` 与游标同时给也报错——两套分页语义混用必然出错。

### TUI 按键与选区

- `mode` 加 `modeVisual`，`onKey` 分派到 `onVisualKey`（只认 `j`/`k`/`y`/`Esc`），`modeLabel` 加 `"VISUAL"`，`fmtStatus` 在 VISUAL 下跟出已选条数。
- `Model` 加 `visualAnchor int`；选区 = `[min(anchor, idx), max(anchor, idx)]`，作用于当前焦点列表。
- `renderMessages` / `renderThread` 的高亮条件从 `r.idx == m.msgIdx` 改为 `m.inSelection(r.idx)`。
- `y` 改为按焦点分派；`Y` 从 `onNormalKey` 删除；`helpText`（[`tui/app.go:919`](https://github.com/amzyang/larkim/blob/b0d84f481a9a18f2cdf39fa3b52ecd7684ae355e/tui/app.go#L919)）同步改写。
- 搜索态（`m.searching`）与 AI/输入 pane 上 `y`、`v` 都只 `notify`，不产出。

## Testing and validation

`go test -race ./...` 为准，新测试全部 in-package、用 testify。

**`agentctx`（纯函数，覆盖 PRD「验收」的主体）**
- 条数与 `<msg-` 出现次数一致；边界后缀在一份输出内唯一。
- 正文含 `</msg>`、`"`、`<` 时结构不破：构造一条正文为 `</msg-xxxx>` 的消息，断言 `Boundary` 重抽后不与正文冲突。
- p2p 出 `## contact`、group 出 `## chat … <n>人` 与 `## people`；`Self` 零值时不出 `me =` 行、正文无 `(me)`。
- 属性矩阵逐项：`reply_to` / `thread` / `edited` / `recalled` / `mentions`（从 `mentions_json` 的 `[{key,id,name}]` 取 `id`）/ `reactions`（含解码失败即省略）。
- 附件行为绝对路径；未落盘出 `(not downloaded)`。
- 多行代码正文原样保留，缩进与空行不被改写。
- `Messages` 为空时仍输出完整头部（对应 PRD 的 0 msgs 降级）。
- `ParseRange`：`200` / `7d` / `24h` / `all` / 非法输入。

**`store`（真 SQLite，`store.Open(filepath.Join(t.TempDir(), "t.db"))`）**
- `BeforeID` / `AfterID` 为开区间：以中间某条为锚，断言结果不含锚点，且 before 段 + 锚点 + after 段无重叠、无遗漏。
- 同一毫秒内多条消息时，行值比较仍按 `(create_ms, message_position, id)` 稳定切分——这是引入游标而非时间窗的全部理由，必须有专门用例。
- `ThreadReplyCounts` 只数 `message_position < 0`，且用 `-3` 而不是 `-1` 做夹具，免得等值判断蒙混过关；`ContactsByIDs`、`ResourcesForMessages` 的批量与缺失行为。

**`tui`**
- `v` 进入/扩展/`Esc` 取消/`y` 后退出，状态栏出 `VISUAL n msgs`。
- 高亮覆盖整个选区而不只是光标行。
- chats pane 的 `y` 范围为 24h 内最多 10 条；话题回复不进 chat 上下文，但 thread pane 的选区里在。
- 搜索态、AI pane、输入框上的 `y`/`v` 只 notify 不复制。
- 未打开会话时 `y` 不写剪贴板。

**手动验证**：`./larkim --config ./dev.yaml tui`，在一个含图片与话题的真实群里按 `y`，把产出粘进 coding agent，确认尾部命令能原样执行；同时取一条真实 `reactions_json` 核对解码。

## Risks and mitigations

- **`reactions_json` 形状未经仓库内验证。** 解码失败即省略属性，不让一条脏数据毁掉整份上下文；实现第一步是取真实样本补一个解码用例。
- **`v` 选区落在话题回复上。** messages pane 里光标本来就能停在话题回复的行上。规则定为：范围查询排除话题回复，显式选区照复——否则会出现「选了 3 条、复制了 0 条」。
- **剪贴板不设上限是既定决策。** `:copy all` 在大群上是几 MB，OSC 52 下会被终端静默截断。状态栏的字节数是唯一的可观察信号，因此 `notify` 的体积必须是真实写入长度而非估算。
- **`Deps` 新增 `DataDir` / `ConfigPath` 后，`New(Deps{})` 的既有测试仍需可用。** 两者为空时附件路径退化成相对路径、续查命令省去 `--config`，不 panic。
