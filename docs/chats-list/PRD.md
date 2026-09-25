# Chats list

TUI 左侧会话列表按飞书桌面端的信息密度重做：头像 + 两行文本，一眼能看出「哪些会话需要我处理」。

## 验收标准

扫一眼 larkim 的会话列表就能分活，不必切回飞书客户端确认。具体到可观察的判据：**@我、未读、草稿、发送失败**四类信号在首屏均可见且准确。

## 布局

```
┌────────────────────────────────────┐ 38 列（含边框）
│ Chats³                            ●│
│ ████ 监控告警  BOT          2 08:23│
│ ████ 应用demo.svc.push.AI-Notif…   │
│ ████ 李明 (01)  On Leave      08:05│
│ ████ 123456u8op-/:;()~'"@)         │
│ ████ 【语言】示例问题及需求…  昨天 │
│ ████ 周舟: [图片][图片]模板… 󰂛    │
└────────────────────────────────────┘
```

列预算：pane 外宽 38，去边框 2 得 36 列内容区；头像 4 列 + 间距 1 列；文本区 31 列。每个会话固定两行。

### 头部

`Chats<未读会话数>` 左对齐，`<免打扰提示点>` 右对齐。头部是整份列表的概览，回答「还有多少个会话在等我」，不是「还有多少条消息」——每条会话的消息数已经在各自行上。

- **未读会话数**：带未读的会话个数，上标红色数字，免打扰会话不计入；为 0 时不画。超过 99 画 `⁹⁹⁺`。上标把数字挂在 pane 名后面，读作一个词（`Chats³`），而不是两个字段。
- **免打扰提示点**：免打扰会话里有未读时画，淡色 `●`，不带数字——被静音的会话不值得被计数。它落在每行 mute 图标右对齐的同一列，免打扰这件事整列读下来。
- 过滤（`/`）只收窄可见行，不改变这两个信号：它们说的是整份列表的状态。
- **窗口标题**：未读会话数同时投射到终端窗口标题——无未读时 `larkim`，有未读时 `(3) larkim`，超过 99 画 `(99+)`。计数走在名字前面，因为标签栏从尾部截断，挂在名字后面的徽章是最先被丢掉的那一段。免打扰不投射：静音就是要求不被拉扯，而标签栏正是那一下拉扯。

上标数字与 `●` 均为单列宽。名字（含过滤串）承担截断，两个信号不让位。

### 第一行

`<会话名> <BOT> <状态标签>` 左对齐，`<未读数> <时间戳>` 右对齐。

- **会话名**：`chats.name`；为空时用 `chat_id`。p2p 对方的企业邮箱前缀带数字尾号时（`liming01`），名字后追加该尾号：`李明 (01)`。尾号不参与截断，截断只作用于姓名部分。
- **BOT 徽章**：仅 p2p 会话，看 `chats.p2p_target_type = 'bot'`。群聊的机器人发言标在第二行的发件人名后。
- **状态标签**：p2p 对方的个人状态（`On Leave`、`出差`、`会议中`），取 `personal_status.title`。仅 p2p 会话显示。
- **未读数**：主消息流里飞书仍报未读、且在 larkim 里也没被读过的计数（`read_state.is_read_remote = 0` 且 `local_read_at = 0`），未读红；免打扰会话画成淡色。头像上画得出徽章时不重复画这个数字；画不出的会话（解码失败、非 kitty 终端）落回文字，两种画法同色。thread 回复不计——见「排序与可见性」。
- **时间戳**：会话最新消息时间，按自然日分档——今天 `HH:mm`，昨天 `昨天`，本周内 `周一`…`周日`，本年内 `MM-DD`，跨年 `YYYY-MM-DD`。

宽度不足时右对齐部分先占位，BOT 与状态标签保留，会话名承担截断（尾部 `…`），下限 2 列。

### 第二行

`<自己的槽><别人的槽> <发件人>: <摘要>` 左对齐，`<mute 图标>` 右对齐。

两个标记槽各占 2 列（图标 + 分隔空格），不成立时不占位：

| 槽 | 令牌（按优先级） | 判据 |
|---|---|---|
| 自己的 | 发送失败 > 草稿 | `drafts.failed_at != 0` / `drafts.text != ''` |
| 别人的 | @我 > 最新表情回复 | `mentions_json` 含 self open_id 且该消息未读 / `reactions_json` 最新一项 |

**发件人前缀**：p2p 不显示对方名字；群聊显示 `发件人: `，该条 `sender_type = 'app'` 时名字后带 BOT 徽章（`Factory: `）。最新一条是自己发的时候，两种会话都显示 `你: `。

**摘要**：优先用 `messages.content`（lark-cli 渲染过的人读文本）压成单行；纯媒体类型或 `content` 为空时回退到 `msg_type` 占位符（`[图片]` `[文件]` `[语音]` `[卡片]` `[合并转发]`）。

**mute 图标**：`is_muted` 为真时显示，淡色。`is_mute_at_all` 不单独表现。

### 空态与异常态

第二行按原因分开措辞，每种空态自我解释：

| 情形 | 文案 |
|---|---|
| 会话无任何消息 | `New chat` |
| 最新消息 `rendered_at = 0` | 淡色 `…` |
| 会话有 `sync_error` | `history unavailable` |
| 最新消息被撤回 | `<发件人>撤回了一条消息`，淡色；会话位置不变 |

## 排序与可见性

按会话最新消息时间倒序，时间相同再按会话名、`chat_id`，末键唯一：没有消息的会话两个时间键都是 0，占列表的绝大多数，仅靠会话名分不开的那些必须有个定序，否则每次查询回来的次序都可能不同。时间口径是本地已同步的消息，backfill 未完成或受限的会话因此位置偏后——这类会话的第二行会显示 `history unavailable`，位置偏差可解释。

**未读不改变位置**，只画徽章。读掉一个会话是关于读的人的消息，不是关于这个会话的消息；把未读做成排序键，光标落到哪一行就会重排哪一行，列表在浏览它的那只手底下移动。

**thread 回复不参与**：`message_position` 为负的消息既不计未读、不作为「最新消息」渲染，也不影响排序；thread 根消息的 position 非负，照常参与。thread 存在的意义就是回复一个久远话题不必惊动整个会话，把它顶回列表顶端正是它要避免的。列表显示的永远是点进去在主消息流里能看到的那一条。

**免打扰只改变画法与概览口径**：muted 会话在列表里照常计未读，只是计数画成淡色（头像上的徽章走飞书自己的灰）。头部的概览不同——它把 muted 会话排除在计数之外，只用一个淡色点说明「静音的那些也有东西」。

## 数据来源

| 展示元素 | 来源 | 刷新时机 |
|---|---|---|
| 会话名 / 模式 / p2p 对方 | `im +chat-list` | 现有 chats 全量刷新 |
| 群头像 | `chats.avatar_path` | 现有资源下载 |
| p2p 头像 | `contacts.avatar_path`，经 `chats` 的联系人 join 取出 | 联系人详情回填 |
| p2p 对方账号尾号 | `contacts.enterprise_email` 的前缀，同一个 join | 联系人详情回填 |
| 最新消息 | `chats.last_*` 冷存列 | 见「冷存摘要」 |
| 未读数 | `read_state.is_read_remote = 0` 且 `local_read_at = 0` 且 `messages.message_position >= 0` | 随同步轮询；打开会话时对该会话立即重查一次，并把该会话页面上的未读整批记为本地已读（thread 回复也在页面上，一并记），同时后台把飞书客户端导航到该会话，让它自己的红点也落下来（见 [read-sync](../read-sync/PRD.md)） |
| mute | `lark-cli api POST /open-apis/im/v1/chat_user_setting/batch_get_mute_status --as user` | 随 chats 全量刷新 |
| 个人状态 | `lark-cli contact user_profiles batch_query`，`query_option.include_personal_status = true` | 随联系人刷新 |
| 草稿 / 发送失败 | `drafts` 表 | TUI 写入 |

**mute** 单次最多 100 个 chat_id，user 身份；非成员与非法 id 走响应的 `invalid_id_list`，视为未知、不画图标。只查最近 30 天有消息的会话——再往下滚 mute 图标一律不画。

**个人状态**只查有 p2p 会话的联系人。返回的 `effective_interval` 带生效区间，渲染时拿当前时间比对，过期即不画：同步滞后只会少画，不会把销假回来的人一直挂着标签。

**联系人身份字段**（姓名、企业邮箱、部门）经 `lark-cli contact +search-user --user-ids` 批量回填。`--user-ids` 收 100 个，但服务端每次只回 20 条并置 `has_more`，所以按 20 个一批发。它答的是全租户，服务端没返回的 id 也记为已查，不每轮重问。

**头像 URL** 只有 `contact/v3/users/{id}` 提供，而它受应用的通讯录数据权限范围约束：范围外的用户返回 `41050 no user authority`，其头像列永久走文字块。范围内的照常。这是后台配置的边界，larkim 侧无从绕过——`+search-user`、`+get-user`、`profile/v2 user_profiles` 和 IM 域都不返回头像。

## 存储

### chats 冷存摘要列

`loadChats` 在每批变更后重跑（`store.Watch` 最密 500ms 一次），最新消息不能靠相关子查询取。回填放在 store 层：`UpsertMessages`（覆盖 ingest、编辑、撤回）和 `UpdateRendered`（覆盖渲染补齐）在各自的事务里重算受影响会话的摘要。这两条是仅有的写 `messages` 的路径，同一原子边界内完成，新增调用方也漏不掉。

新增列：`last_message_id`、`last_message_ms`、`last_sender_id`、`last_sender_name`、`last_msg_type`、`last_summary`、`last_deleted`、`muted`、`mute_checked_at`。

`message_count` 仍为相关子查询，是本次改动后 `ListChats` 上剩余的唯一逐行开销。

### contacts 新增列

个人状态：`status_title`、`status_icon_key`、`status_start_ms`、`status_end_ms`、`status_checked_at`。

账号标识：`enterprise_email`（存全量，渲染时取前缀的数字尾号）、`is_cross_tenant`。

### drafts 表

消费者写的表，与 `read_state.local_read_at` 同类，daemon 不碰。

| 列 | 含义 |
|---|---|
| `chat_id` | 主键 |
| `text` | 未发送的 composer 内容 |
| `reply_to` | 草稿关联的回复目标 |
| `in_thread` | 回复是否落在 thread 内 |
| `failed_at` | 非零表示上次发送失败，内容已回到草稿；用户再次编辑时清零 |
| `updated_at` | 最后写入时间 |

只在切会话、失焦、退出时落盘，不是每次敲键都写。两个 TUI 同时开同一会话时后写赢，不做冲突检测。

`docs/SCHEMA.md` 随这些列一并更新，含 `drafts` 的所有权声明。

## 头像渲染

kitty 图形协议画真实头像，`VirtualPlacement` + Unicode placeholder（U+10EEEE + 变音符编码行列，image id 走前景色）：图像作为文本单元格内容随列表滚动，不需要每帧重新放置。ultraviolet 把这个字形簇算作单列并原样透传，行轮转时图像跟着各自的 placeholder 走。

- **传输时机**：必须在 alt screen 建立之后。虚拟放置绑定屏幕缓冲区，进 alt screen 前传的图在 alt screen 里不存在。用 `tea.Raw` 发 APC——它落进渲染器自己的输出缓冲、共用同一把锁，不会撕帧。
- **传输范围**：可视区及其前后各一屏，滚动时增量传入，滚出范围的 image id 由 LRU 回收；同一 id 上重传即替换，不必先删。id 取 16 起，免得调色板降级把它折成具名 ANSI 色。
- **预处理**：`image/png` + `image/jpeg` 解码，`golang.org/x/image/draw` 缩到 64×64，按 `c=4, r=2` 交给终端。不做圆形遮罩。
- **降级**：非 kitty 终端、webp、解码失败、`avatar_path` 为空或哨兵值（`'none'` 无头像、`'-'` 下载失败不重试）——一律走文字块：2 行高的色块，底色由 `chat_id` 稳定哈希到 ANSI 调色板，中间放会话名首字。解码失败的会话记住结果，不每帧重试。
- **选中态**：高亮只覆盖文本半边。头像代表图片，选中不该给它染色，kitty 图片也染不动。

渲染层是 `avatarRenderer` 接口，文字块与 kitty 两个实现。

## 交互

- **滚动与命中**：会话是最小单位。`chatTop` 仍是会话索引，一次滚一整个会话；`hit(x, y)` 用 `(y - 1 - headerHeight) / 2` 取会话。底部剩余高度不足两行时留空，不画半个会话。
- **过滤**：`/` 的匹配范围不变，只匹配会话名和 `chat_id`。找消息走 `:search`。
- **草稿**：切会话保留 composer 内容与 `replyTo` / `inThrd`，重开 TUI 仍在。发送成功后清除该会话的草稿行。

## 终端要求

- **Nerd Font 为前置约束**，README 写明。整个 TUI 统一用图标字体，不提供 ASCII 降级集。
- `minWidth` 抬到 78（chats 38 + messages 40）。`minHeight` 不变。

## 测试

- 数据层与排版走白盒测试：摘要拼接、时间戳分档、截断优先级、槽位令牌、`你: ` 前缀规则、四种空态文案、thread 回复过滤、撤回渲染、账号尾号的提取与免截断。
- `avatarRenderer` 的文字实现全测；kitty 实现只测纯逻辑部分——占位宽度为 4 列、image id 的分配与淘汰。
- kitty 图像回归用一个 e2e：探测到 `KITTY_WINDOW_ID` 且 `kitten` 可用就在真窗口里跑 TUI 截图比对，否则 `t.Skip`。`go vet` + `go test` 的提交门槛不变。

## 非目标

- 顶部的 feed shortcuts 横条。快捷入口在终端里不如 `/` 过滤和 `:goto` 快。
- 日程 / 会议提醒条（数据在 calendar 域）。
- Collapsed Chats 折叠会话（OpenAPI 未暴露）。
- 飞书端置顶影响列表排序——它在飞书端是独立的快捷入口，本来就不参与排序。
- 群成员的个人状态标签，只做 p2p 对方。
- 过滤匹配发件人或消息摘要。
