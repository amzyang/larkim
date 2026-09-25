# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Design

- using modern kitty terminal capability
- CLI: 设计参数时必须考虑 shell 自动补全（命令、子命令、flag、参数值）
- TUI: 交互设计必须考虑补全（输入时的候选提示与选择）

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
- lark-cli 调用在互斥锁下串行化（token 刷新走跨进程文件锁、限流按用户），不要并发调用

## Data contracts

- Migrations 为 `store/migrations/NNNN_name.sql`，只增不减，无 down migration
- `docs/SCHEMA.md` 是对外契约（直连 SQLite 的消费者依赖它），改列必须同步更新
- 时间戳一律 Unix 毫秒 UTC；消息顺序 `ORDER BY create_ms, message_position, id`；`message_position = -1` 表示 thread 回复
- FTS5 用 trigram 分词（unicode61 把整段 CJK 当一个 token），MATCH 仅对 ≥3 字符词有效，短词走 `instr` 回退
- 单写者：只有持 `daemon.lock` 的进程写同步数据，其他进程只能写 `read_state.local_read_at`；外部消费者的处理进度由消费者自持，库里不记
- 发给飞书的时间必须用 `larkTimeLayout`，绝不输出 `Z`（`messages/search` 原样转发）

## Telemetry

- Sentry 只上报缺陷：`reportable()` 过滤用户错误、`ErrNotFound`、认证/网络/限流类 lark 错误、`AmbiguousError`、context 取消
- panic 捕获后必须 re-panic，保留退出码

## Style

- 注释与代码用英文；注释写 why 不写 what
- 依赖注入用导出字段的 struct（如 `Syncer`、`cli.App`），接口定义在消费方
- 错误用 `%w` 包装，`errors.As` 分类；`*larkcli.Error` 提供 `IsAuth/IsNetwork/IsRateLimit/IsPermanent`
