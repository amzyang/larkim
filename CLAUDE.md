# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Design

- using modern kitty terminal capability
- CLI: 设计参数时必须考虑 shell 自动补全（命令、子命令、flag、参数值）
- TUI: 交互设计必须考虑补全（输入时的候选提示与选择）

## Interaction

交互行为与 UI/UX 以 Lark 桌面客户端（英文界面）为基准：它是成熟且经过大量验证的 IM，默认照抄它的语义，不自创。

- 允许做它的子集（少功能、少形态），不允许做它的反面：同一动作的结果、方向、默认值、术语不能与客户端相反
- 新交互先确认客户端怎么做，再决定我们做不做、做到哪一层；找不到对应行为时才自行设计，并在注释里写清为什么无对应
- 客户端概念的命名与语义直接沿用其英文 UI 字符串（Chat、Group、Thread / Reply in thread、Mention、Unread、Mute、Pin、Buzz、Recall、Reaction），不换词、不改含义、不回译成中文；代码标识符、TUI 可见文案、注释与 docs 统一用这套英文词，拿不准的词以客户端里的实际字符串为准
- 客户端数据带中英双份名称时（emoji 名、显示名）取英文那份；中文名只在匹配用户输入（搜索、补全）时作为额外的候选
- TUI 无法模拟或成本过高的（富文本编辑、悬停、动画、内联图片、拖拽）降级为最接近的子集：保留同一心智模型，删表现形式，不改语义
- 键位遵循 TUI 习惯（Bubble Tea/vim 式导航），但键位触发的动作语义与客户端一致；两边习惯冲突时以 TUI 习惯定键、以客户端定行为
- 跳到客户端一律用原生 scheme `lark://`（`lark://applink.feishu.cn/client/chat/open?openChatId=…`、`lark://vc.feishu.cn/j/…`），不用 `https://` applink：后者先开浏览器标签页再重定向回客户端

## Architecture

- 单人自用工具，不分发：除持久化的数据与状态（SQLite 已落盘数据、`dev.yaml` 配置、`docs/SCHEMA.md` 对外契约）外，只要能重建就不考虑向后兼容，直接用最简单直接的策略
- 派生物（FTS 索引、汇总/缓存表、TUI 状态、构建产物）坏了就重建或重新 sync，不写兼容层、不留迁移期 fallback
- 运行环境只考虑本机（macOS + kitty + 已安装的 lark-cli），不为其他 OS、终端、Go 版本或未安装依赖做适配，除非需求明确要求

## Privacy

- 测试数据来自本人真实飞书账号，含个人、同事与公司信息：真实内容只留本机（`~/.larkim`、`dev.yaml`、`/plans/` 均已 gitignore），进仓库的一切（测试、docs、issue 复现、commit message）只能是虚构数据
- 人物用固定化名：`林岚`（self）/`张三`/`李四`/`王五`/`构建机器人`，邮箱 `@example.com`，home 路径 `/Users/linlan`；群名用泛化职能名（`平台组`、`项目协作群`）
- ID 写语义化短假值（`oc_quiet`、`om_elsewhere`、`ou_a`、`cli_c`）；真实 ID 形如 `om_x100b6473dc29d8b0c10f2c17de29001`、`img_v3_0215r_…`，一眼可辨，不要粘进来
- 把 lark-cli 响应、SQL 结果、TUI scrollback 贴进 docs 或 issue 前逐项替换：message/chat/user/file key、人名、群名、邮箱、手机号、消息正文；不含身份的聚合数字（行数、分组计数）可原样保留
- 复现用最小构造用例，不整段粘真实会话；不新增追踪真实导出或 db dump 的文件

## Commands

- Build: `just build`（输出 `./larkim`）；Test: `just test`；Vet: `just vet`
- 本地验证用 `go test -race ./...`（CI 不带 -race，但代码有真实并发）
- 提交门槛仅 `go vet` + `go test`，不引入 golangci-lint / gofumpt
- 本地跑 sync 需自建 `dev.yaml`（不在仓库中）：`./larkim --config ./dev.yaml sync`

## Tests

- 新功能必须附带测试
- 测试 in-package（白盒），用 testify `require`/`assert`
- 飞书边界用 `larkcli.Fake`（非 _test 文件，可跨包导入）；时间用 `sync.Clock` 假时钟
- 数据库不 fake：`store.Open(filepath.Join(t.TempDir(), "t.db"))` 打开真 SQLite，migration 自动执行
- 测试命名 `TestSubject_BehaviourDescription`

## Commits

- Conventional Commits，英文 subject + 英文 body，body 说明动机

## Dependencies

- Bubble Tea/Bubbles/Lipgloss 用 `charm.land/...` v2 模块路径，不是 `github.com/charmbracelet/...`
- SQLite 用 `modernc.org/sqlite`（纯 Go），构建 `CGO_ENABLED=0`
- 无飞书 Go SDK：所有 API 访问经外部 `lark-cli` 子进程（`larkcli.ExecClient`），认证由 lark-cli 管理，larkim 不存凭据
- lark-cli 调用经 `larkcli` 的计数 lane 限流，背景清扫、屏幕节拍、按键各一条，lane 宽度即并发上限；
  lane 用 `larkcli.WithLane` 挂在 context 上，不写进方法签名
- lark-cli 自身没有客户端限流器，飞书频控按「每 API × 每应用 × 每租户」分级计，所以 lane 宽度是 larkim
  唯一的速率控制点；加宽前先确认目标端点的频控等级
- `go generate ./emoji` 需要已安装的飞书客户端（读它的 emoji 资源）与 `uv`（`uv run --with pypinyin`
  给词表注音）：go-pinyin 逐字查表、多音字只取第一个读音，把音乐读成 yinle、调皮读成 diaopi。
  只有 `table.go` 的生成走 Python，产物入库，运行时仍是纯 Go；会话名与人名的拼音仍走 go-pinyin

## Data contracts

- Migrations 为 `store/migrations/NNNN_name.sql`，只增不减，无 down migration
- `docs/SCHEMA.md` 是对外契约（直连 SQLite 的消费者依赖它），改列必须同步更新
- 时间戳一律 Unix 毫秒 UTC；消息顺序 `ORDER BY create_ms, message_position, id`；`message_position = -1` 表示 thread 回复
- FTS5 用 trigram 分词（unicode61 把整段 CJK 当一个 token），MATCH 仅对 ≥3 字符词有效，短词走 `instr` 回退
- JSON 列（`mentions_json`、`reactions_json`、`chats.last_*_json`）一律存最小化形式，由 store 的 `compactJSON` 在写入时保证；`namesSelf` 按文本匹配 id 就靠这条。lark-cli 的输出是缩进的，绕过 `UpdateRendered`/`UpdateReactions` 直接写这几列会让 @我 标记和 `:mentions` 面板静默失效
- 单写者：只有持 `daemon.lock` 的进程写同步数据，其他进程只能写 `read_state.local_read_at`；外部消费者的处理进度由消费者自持，库里不记
- 发给飞书的时间必须用 `larkTimeLayout`，绝不输出 `Z`（`messages/search` 原样转发）

## Telemetry

- Sentry 只上报缺陷：`reportable()` 过滤用户错误、`ErrNotFound`、认证/网络/限流类 lark 错误、`AmbiguousError`、context 取消
- panic 捕获后必须 re-panic，保留退出码

## Style

- 注释与代码用英文；注释写 why 不写 what
- 依赖注入用导出字段的 struct（如 `Syncer`、`cli.App`），接口定义在消费方
- 错误用 `%w` 包装，`errors.AsType[T]` 分类；`*larkcli.Error` 提供 `IsAuth/IsNetwork/IsRateLimit/IsPermanent`
- 写或改 Go 代码前先调用 skill `/modern-go-guidelines:use-modern-go`，用它的 `list` 取当前 Go 版本（go.mod 为 1.27）的惯用法清单并照做；与周边旧写法冲突时以清单为准，只有「编译不过 / 改变行为 / 明显不适用」才跳过（跳过前先 `explain` 该条）
- 该清单里本仓库高频命中的：`errors.AsType[T]` 取代 `errors.As` 临时变量、`wg.Go`、`t.Context()`、`for i := range n`、`cmp.Or`、`slices`/`maps` 的迭代器版本（`SplitSeq`、`slices.Collect`、`slices.Sorted`、`maps.Keys`）、`min`/`max`/`clear`、typed atomics、`new(v)` 取代临时变量取址
