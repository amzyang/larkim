# Containers — 技术方案

行为见 [PRD.md](PRD.md)。

## 两种容器不同构

这是整份方案的前提，也是唯一一个不能照直觉走的地方。

话题回复取自 `GET /im/v1/messages?container_id_type=thread`：

```
id=om_r1  pos=-3  thread=omt_1  parent=om_root  root=om_root  chat=oc_a(本会话)
```

它是本会话的新消息：自己的 id、负位置哨兵、三个归属字段。它本来就在 `messages` 里，`pullChat`（sync/syncer.go:823）和开着话题时的心跳（sync/focus.go:29）都在拉它。

会话容器的 listing 只给根，不给回复，而 `pullChat` 只对**本次窗口里见到根**的话题发这个请求。于是回复一个久远话题——话题存在的理由——后台拉不到：repair 的地平线是 7 天，再往前只有 backfill 走过一次。`pullStakedThreads`（sync/syncer.go）补上这一段：跟着 slow path 的节拍，按 `StakedThreads` 取我有份的话题里最新的 `stakedThreadsTopK` 条，逐个按 id 拉它的容器。按最新活动倒序取前 K 是自限的，安静下来的话题自己掉出窗口，所以不必为轮询顺序另存游标。代价是话题容器不认 `start_time`（给一个晚于全部回复的值，它照样全返回），每轮都是整条话题重列一遍。

合并转发的子消息取自 `GET /im/v1/messages/{root_id}`，一次调用返回全部嵌套层级的扁平 `items`：

```
[0] merge_forward  id=om_fwd   chat=oc_a(本会话)   pos=612   upper=(缺)
[1] text           id=om_orig  chat=oc_src(源会话) pos=(缺)  upper=om_fwd
```

拿 `om_orig` 去 `messages/mget` 会返回 `chat=oc_src pos=10 upper=(缺)`——**同一行数据，两种上下文两套字段**。转发不复制消息，只是引用。

所以子消息写进 `messages` 的两条路都是错的：写负哨兵会覆盖源会话真实行的 `message_position`，那条消息在自己的会话里消失；写 0 会被 `UpsertMessages` 的 `CASE WHEN excluded.message_position <> 0` 挡住，但对 larkim 没同步过的源会话，子消息会凭空以 position 0 建出一个会话的主流。

独立表不是权宜：话题回复天然属于 `messages`，转发子消息天然不属于。

两个由此而来的实现约束：

- **不能用「`message_position` 字段缺失」认容器。** `larkcli.RawMessage.MessagePosition` 是非指针的 `intString`，缺失解码成 `0`，与真实的 position 0 不可分。按 `message_id == rootID && upper_message_id == ""` 认。
- **子消息的图片挂在 bundle 上。** `ExtractRendered`（sync/resources.go:97）从渲染串抠 `img_…` 并注册到转发本身的 id，因为资源端点拒绝子消息的 id。展开渲染时要把 bundle 的资源按 `FileKey` 分派回各子消息，否则一张图都画不出来。

## 一帧只画一层

嵌套转发不在面板内缩进渲染，而是每层一帧。一个转发帧只装 `upper_message_id` 指向本帧的那些子消息；其中 `msg_type = merge_forward` 的画成卡片，点它压下一帧。

这让帧内内容永远是平的，于是子消息映射成 `store.Message` 后可以原样交给 `renderRows`——图片、富文本、卡片、附件全部继承既有渲染。44 列也不必为缩进让路。映射时 `MessagePosition = 0`、`ThreadID = ""`，子消息不得再开话题帧。

帧一次画完，不分页。转发是冻结的、有界的，不会在读者眼前长大；分页是为无限流准备的机制。

## 右栏：可见帧摊开，只压被盖住的帧

右栏今天是三个互斥布尔（`threadOpen` / `infoOpen` / `aiOpen`，tui/app.go:1226 的 `rightOpen`）。改成栈时，**可见的那一帧保持摊开在原有字段里，只把被盖住的帧入栈**：

```go
rightKind  rightKind // what the unpacked fields mean; rightNone when closed
rightStack []rightFrame
```

代价是 `push`/`pop` 各多一次字段搬运，换来的是 `renderThread`、`rebuildThread`、`layout`、`scrollRight`、`visiblePanes`、`selectedZones`、`metaFor`、`holdTop`/`topAnchor`/`atTail`、`onClick` 的右栏分支全部不动，`rightOpen()` 与 `View()` 的分派也不动。

`threadOpen` 从字段变成 `rightKind != rightNone` 的方法，读作「右栏正显示一个消息列表帧」。留成字段就有两个真相要同步：只置 `threadOpen` 的调用点会造出一个 `loadRight` 认作关闭、`rightOpen()` 认作打开的 model。

`info` 与 `ai` 是根视图，永远不会被盖住，所以 `infoTop` / `aiTop` / `aiText` 保持裸字段不进帧。

**栈是包含路径，不是访问历史。** 从消息面板打开一个容器时，屏幕上那个容器是它的兄弟而不是它的父级，所以那一下**重置**成一层；只有从右栏里打开才加一层。两个入口因此是两个函数：`openRight` 与 `pushRight`。否则连看五个话题要按五次 `Esc` 才关得掉右栏，而那五个彼此无关——那正是 PRD 排掉的「通用的跳回来返回栈」。

一个被压住的帧只留 id，不留任何排过版的东西：

```go
// rightFrame is a suspended frame. Nothing measured is kept — rows and a top
// line number are counted against a width the terminal may have changed while
// the frame was buried, and the lists come back from SQLite in a millisecond.
type rightFrame struct {
	kind rightKind
	// id is what the frame was opened on: omt_… for a thread, and for a
	// forward the message whose children it lists — the bundle itself at the
	// top level, a nested bundle below. Held by id rather than index, like
	// filterPin: a sync tick replaces the list under a suspended frame.
	id string
	// root is the bundle every level of a forward belongs to, which is where
	// its children are stored and its pictures are registered. Empty for a
	// thread.
	root string
	// sel is the message the cursor was on and top the line the viewport
	// started at, so a pop lands where the reader left rather than at the
	// tail.
	sel string
	top lineAnchor
}
```

一层一个 `root` 是必需的：`forwarded_messages` 按 `(root, upper, message_id)` 存，光有嵌套 bundle 自己的 id 查不出它的孩子，而同一个嵌套 bundle 可以坐在两棵树里。

弹出即重载：`pop` 读出 `id` 重发该帧的加载命令，`sel` 与 `top` 交给既有的 `repinSelection` 与 `holdTop` 落位。两种帧的加载都只读本地库，所以「弹出时空一帧再填」这一瞬不存在。

**`Model` 是值类型，每次改栈必须先 `slices.Clone`。** 现有字段没有一个是切片就地改的；直接 `append` 到共享底层数组，会让一个被丢弃的 model 的 push 串到活的那个上。这是本期最可能出的隐蔽 bug。

**`pushRight` 必须对栈顶去重。** `pressZone` 的注释（tui/app.go:2256）说明双击不去重——这对表情是对的（加了又取消，与客户端一致），对压栈是错的：会叠出两帧、要按两次 `Esc`。

**`pushRight` 要夺回焦点。** 消息面板的点击在 `onClick`（tui/app.go:2200）里已把焦点设成 `paneMessages`，压栈后要改回 `paneThread`，照 `toggleThread`（tui/app.go:1890）的做法。

逐点改动：

| 位置 | 改法 |
| --- | --- |
| `focusMessages` tui/app.go:2171 | 窄屏时清空整栈，不是弹一层——读者是要离开右栏 |
| Esc 阶梯 tui/app.go:1524 | `m.threadOpen && focus == paneThread` 那一臂改为弹一帧；阶梯仍然手写。焦点不在右栏时 Esc 不碰栈，照旧落到 `chatFilter` 与 `setReply(nil)` |
| `toggleInfo` tui/info.go:32、`closeAI` tui/app.go:2181 | 清空整栈后再开自己 |
| `toggleThread` tui/app.go:1876 | 改为 `toggleRight`：可见帧已是该容器则关，否则从消息面板 `openRight`、从右栏 `pushRight` |
| `openThreadID` tui/chatpoll.go:63 | 可见帧是话题帧则取它，否则自栈顶向下找最近的话题帧——转发压在话题上时，下面那个话题仍有回复要拉 |
| `activate` 的 `paneThread` 臂 tui/app.go:1868 | 按可见帧类型分叉：话题帧回复，转发帧打开选中的嵌套转发 |
| `reloadCurrent` tui/app.go:1067 | 只重载可见帧；被压住的帧在弹出时重载 |

## 回复树：聚起来看，不搬走

话题与转发都是折叠——内容离开主流，摘要顶替它。回复树不是：`reply_to` 的回复是本会话的普通消息，`message_position` 非负，本来就该在主流它自己的时间点上（本机 1,643 条回复无一例外）。Details 帧把散着的一串聚起来，主流那边一行不动。

于是三处与另外两种容器相反：`messageQuery` 不加过滤条件，摘要行不报最后一条回复（回复就在下面摆着），落地也不结算未读（读会话时已经结算过）。

### 两个递归 CTE

计数看整棵子树，不是直接回复数——客户端的「5 replies」就是这么数的，本机实测树深到 6 层。

`ReplyGists`（store/messages.go）一页一次，两步：

```sql
-- 从页上每条消息向上走到本地存着的最上面那条
WITH RECURSIVE up(seed, id, parent) AS (
 SELECT message_id, message_id, reply_to FROM messages WHERE message_id IN (…)
 UNION ALL
 SELECT u.seed, m.message_id, m.reply_to FROM messages m JOIN up u ON m.message_id = u.parent
) SELECT u.seed, u.id FROM up u
 WHERE u.parent = '' OR NOT EXISTS (SELECT 1 FROM messages p WHERE p.message_id = u.parent)
```

父消息没同步过的那种（本机 14 条）也认作根：本地看得见的最上面一条，就是这里能给出的根。

第二步拿这些根向下收全部后代，`JOIN messages` 后按 `deleted = 0` 计数。**穿过撤回的消息，但不计它**——中间一条被撤回不能让挂在它下面的回复失去归属。`UNION` 去重，环不成立。

`ReplyTree` 是同一个向下的 CTE 配 `messageColumns`，按 `create_ms, message_position, id` 排——根最老，自然排在第一行，正是客户端 Details 顶上那条。

`store/migrations/0033_messages_reply_to.sql` 给 `reply_to` 建部分索引，照 `messages_thread` 的样子。这查询挂在每次 `revMsg` 的页面重载上，没有索引时递归的每一层都是一次 `messages` 全表扫描。

向下那步的 `ON` 里多一个 `m.reply_to <> ''`，而它是 join 本身就蕴含的——message id 不可能为空。**部分索引只在查询能证明这一行落在索引里时才会被选中**：少了这个项，SQLite 把 `m` 放外层做 `SCAN m` + `SCAN d`，本机 200 个根 1.1s；加上它走 `SEARCH m USING INDEX messages_reply_to`，7ms。

### 摘要行的位置

`threadSummary` 与 `replySummary`（tui/summary.go）都画在 `reactionRows` **之后**，是一条消息最外面的两行：

```
张三  1
       5 replies
```

李四  hello
      ⤷ 23 replies · 王五: 1234

表情贴着消息本身，这两行指向别处的回复，所以在更外面。两行都什么也没顶替——根消息的正文照常整条画出来，折走的只有回复——所以它们是脚注而不是容器的头，客户端也把它们放在气泡下面。

`st.inFrame` 与话题摘要共用同一道闸：帧里回复就排在根下面，再数一遍什么也没说。`x.ThreadID != ""` 时不画，话题回复在话题帧里读。

### 从树里任何一条打开

`ReplyGist.Root` 对页上每条消息都有值，所以 `detailsAtCursor`（tui/right.go）不要求光标停在根上。根常常比当前这页更老，而读者指着眼前这条问的是同一场对话。

`containerAtCursor` 不动，`toggleRight` 在它落空后才问 `detailsAtCursor`：`Enter` 只读前者，所以一条有回复的消息按 `Enter` 仍然是回复它。话题根不同——那里的回复要进话题里发，「打开」和「去回复」是同一件事。

帧标题是根消息的一行摘要。从根打开时光标手上就有，从别处打开时空着，等 `onReplyLoaded` 拿到树再填（tui/replypane.go），与转发卡片补标题是同一种做法。

右栏里 `Enter` 的 `inThread` 随帧而定：`m.rightKind == rightThread`。Details 帧里发出去的回复带 `reply_in_thread` 会给一条根本没有话题的消息开话题。

## 点击

`clickZone`（tui/rows.go:64）加一个 id 配一个判别式，沿用 `jump` 的先例：

```go
// open is the container this row leads into: a thread's id for a root's reply
// summary, the bundle's own id for a merged forward. It is not a place to open
// but a pane to push.
open     string
openKind rightKind
```

一个 id 加 `openKind`，不靠 `omt_` / `om_` 前缀分派——仓库里没有任何地方按 id 前缀决定行为，那不是保证。`live()`（tui/rows.go:88）学会这个字段，`pressZone`（tui/app.go:2260）加一臂 `pushRight(z.openKind, z.open)`。

整行一个 zone，`x0 = lead.cols()`，照 `quoteRow`（tui/rows.go:485）的做法。

两个面板的点击天然一致：`onClick` 在调用共享的 `pressZone` **之前**就付掉了各自的 x 偏移（tui/app.go:2227 与 2236），新的一臂完全不看 `p`。

单击即开：`onClick` 先查 zone 再判 double，命中 zone 就 return，所以既不移动光标也不触发 `activate`，与链接、引用行的现有行为一致。

`selectedZones`（tui/app.go:1216）跳过 `len(z.urls) == 0` 的 zone，所以新 zone 和 `jump` 一样对 `o` 键不可见。这是对的：`o` 的含义是「打开这条消息带的东西」——链接、附件、飞书本体，而右栏是 larkim 自己的面板。键盘走 `Enter` 与 `t`。

## 数据

`store/migrations/0030_forwarded_messages.sql`：

```sql
-- A merge_forward's children are the original messages of their own chats:
-- they carry the source chat_id and the source message_id, which already
-- exist as real rows in messages. They cannot live there — writing them would
-- either overwrite a real message's position or file them under a chat larkim
-- never synced — so they get a table of their own.
--
-- The key carries upper_message_id because one message can sit at two depths
-- of the same bundle: someone forwarded it alone, and forwarded a stretch of
-- history containing it, and both went into one merge. Keyed without the
-- parent, the second copy would silently replace the first and a level would
-- come up a row short.
CREATE TABLE forwarded_messages (
    root_message_id  TEXT    NOT NULL,  -- the bundle in messages
    upper_message_id TEXT    NOT NULL,  -- direct parent; the root at the top level
    message_id       TEXT    NOT NULL,  -- the ORIGINAL message's id, not a copy
    seq              INTEGER NOT NULL,  -- order among siblings, by create time
    chat_id          TEXT    NOT NULL,  -- the ORIGINAL chat, often one larkim never synced
    msg_type         TEXT    NOT NULL,
    sender_id        TEXT    NOT NULL,
    sender_name      TEXT    NOT NULL,
    create_ms        INTEGER NOT NULL,
    content_raw      TEXT    NOT NULL,
    mentions_json    TEXT    NOT NULL DEFAULT '',
    raw_json         TEXT    NOT NULL DEFAULT '',
    PRIMARY KEY (root_message_id, upper_message_id, message_id)
) WITHOUT ROWID;

CREATE INDEX forwarded_children ON forwarded_messages(root_message_id, upper_message_id, seq);

-- One row per bundle: the work queue, its backoff, and the child count the
-- collapsed summary reads without counting rows. child_count is the top
-- level alone, matching what one frame lists — a number the reader can check
-- against the rows in front of them.
CREATE TABLE forwarded_roots (
    root_message_id TEXT PRIMARY KEY,
    fetched_at      INTEGER NOT NULL DEFAULT 0,
    child_count     INTEGER NOT NULL DEFAULT 0,
    attempts        INTEGER NOT NULL DEFAULT 0,
    next_attempt_at INTEGER NOT NULL DEFAULT 0,
    last_error      TEXT    NOT NULL DEFAULT '',
    FOREIGN KEY (root_message_id) REFERENCES messages(message_id)
) WITHOUT ROWID;

-- Backfill: seed the queue with every bundle already stored.
INSERT OR IGNORE INTO forwarded_roots (root_message_id)
  SELECT message_id FROM messages WHERE msg_type = 'merge_forward' AND deleted = 0;
```

嵌套的 bundle 不进 `forwarded_roots`：一次调用已经带回它的子消息，而它本身不在 `messages` 里，外键也容不下它。队列只装本会话里那些真实的转发消息。

没有 `content` 列：子消息的正文由 `content_raw` 就地渲染（`pendingText` 已经能展 `text` 与 `post`，其余落 `msgTypeLabel`），不为它们再发一轮 `+messages-mget`——那是每条子消息一次调用，不值。

回填是**播种队列**而非游标。`resources` 用 `resource_scan_id` 倒带是因为它没有天然的队列；转发有——它就是 `messages` 里 `msg_type = 'merge_forward'` 的那些行。`attempts` / `next_attempt_at` / `last_error` 照抄 `resources` 的退避列，不另发明。

`docs/SCHEMA.md` 增一节。

## 同步

接入点在 `upsertRaw`（sync/syncer.go:533）——所有存消息的路径都汇到这儿，且 `MsgType` 无需等渲染就已知，连渲染被判空的转发也能入队：

```
upsertRaw → AddForwardRoots(本批里的 merge_forward id)
tick      → expandForwards(有界排空)  ← 与 downloadPending 并列
```

**不挂在 `storeRendered`。** 那里被 `renderPending` 与 `downloadPending` 两条路调用，且已在 50 条一批的循环内，塞一次网络调用会把整个渲染批串行化。

`Opt.ForwardsPerTick` 默认 3，队列按 `create_ms` 倒序取。最新的转发是读者最可能打开的那些，而这就是全部需要的调度：本机存量 39 条，十几个 tick 排空，此后队列稳态为空，真正要「插队」的那一条走下面的即时展开。

`larkcli` 加一个方法，实现照 `MGetRaw`（larkcli/exec.go:397）：

```go
// ForwardedMessages lists every message inside a merged-forward bundle. One
// call answers the whole tree: the items are flat, linked by UpperMessageID,
// and one of them is the bundle itself.
ForwardedMessages(ctx context.Context, rootMessageID string) ([]RawForwarded, error)
```

`RawForwarded` 嵌入 `RawMessage` 再加 `UpperMessageID`，而不是给 `RawMessage` 加字段——那个字段对 99% 的调用点没有意义。id 直接拼进 URL path，与 `AddReaction`、`DeleteReaction` 一致：飞书的 message id 是 `om_` 加十六进制。

Lane 取零值 `LaneBackground`。`LaneBeat`（宽 3）装的是开着的会话的拉取、它的话题和搭车的刷新，在那儿扇出会饿死它存在的理由。

读者打开一个尚未展开的转发时，按 `LaneInteractive` 即时取一次——那条 lane 就是为人留的。帧先压上、画一行「正在展开…」，数据到了就地填充。

API 拒绝（读者已退出源会话，或转发过旧）时按 `*larkcli.Error` 的 `IsPermanent()` 分流：永久失败记 `last_error` 并盖 `fetched_at`，不再重试——转发是冻结的，再问一次答案不会变；临时失败累加 `attempts` 并退避。卡片标题随之写成无法展开，点击给提示而不是开空帧。

### 子消息的附件

子消息的 `content_raw` 自带 `file_key`，`ExtractResources` 原样抠得出来——只是要注册到 **bundle** 的 id 上。资源端点按「这个 key 在这条消息里吗」判权，而子消息的 id 在它自己的会话里；拿子消息的 id 去问，飞书答 `234003 File not in msg.`，拿 bundle 的 id 去问就给字节，图片与非图片都一样。`ExtractRendered` 把渲染串里的 `img_…` 挂在 bundle 上，正是同一条理由。

两条路注册的是同一批 key，`AddPendingResources` 按 key 去重，所以一张图只有一行。

展开渲染时把 bundle 的资源按 `FileKey` 分派回各子消息，否则一张图都画不出来。

## 渲染

话题的正文是**恰好一行**，转发的正文是一张**定长卡片**，两者都由 `tui/summary.go` 的构造器产出：

```go
func threadSummary(x store.Message, idx int, st msgStyle, g *leads) (msgRow, bool)
func forwardSummary(x store.Message, root string, idx int, st msgStyle, g *leads) ([]msgRow, bool)
```

话题返回单行，签名与 `quoteRow`（tui/rows.go:485）一致——它也是「一条消息附带的一行，整行一个 zone」，连 `bool` 的含义（这行该不该画）都相同。

话题行的构成是「数量 · 代表内容」，宽度分配照 `reactionChip`（tui/rows.go:725）的既有取舍：**数量必须活下来，内容按剩余宽度截断**。

```
⤷ 23 replies · 李四: 1234
⤷ No replies yet
```

转发卡片每行都以 `forwardBar`（`▏`，`quoteRow` 已在用的那根竖线）开头，首行是标题（`stBold`），其后是至多 `store.ForwardPreview` 行预览（`stDim`），子消息多于预览时再加一行 `…`。卡片的每一行都挂同一个 zone，所以点中哪一行都是「打开这张卡片」。

```
▏Group Chat History
▏张三: 预算定了
▏李四: 收到
▏…
▏聊天记录
▏Chat History · cannot be expanded
```

标题由 `forwardTitle(g, self, selfName)` 从 `ForwardGist` 的源会话字段算出，四种结果：`Sources == 1` 且源会话在 `chats` 里时按 `chat_mode` 分「Group Chat History」与「{self} and {peer}'s Chat History」，`p2p_target_id` 等于 self 时收成「{self}'s Chat History」；其余写「Chat History」。

兜底不是偷懒：`im chats get` 对一个读者不在其中的会话只回 `bot_count`/`chat_status`/`i18n_names`/`user_count`，既没有 `name` 也没有 `chat_mode`，所以本地查不到的源会话没有第二条路可问。「聊天记录」正是客户端对混合来源的写法，是它的子集而不是反面。

卡片不写条数。客户端的卡片也不写，省略号已经说明帧里装得下更多；未展开的 bundle 因此只画标题一行，不从 `content` 数 `[时间] 姓名:` 的行数补数字——那棵字面树含全部嵌套层，与 `child_count` 的顶层口径不同。

`msgTypeLabel`（tui/chatrow.go）里 `merge_forward` 写 `[Chat History]`，会话列表的 `lastMessageSummary` 与引用行的 `replyGist`（tui/replybar.go:183）都在读 `content` 之前短路到它——`messages.content` 里那棵树留着（`yc` 要全文），压平成一行就是标签加 ISO 时间戳。

代表的那几条，话题取尾、转发取头。预览内容复用 `replyGist` 的降级链，发送者名走 `displaySender`（tui/rows.go:513），截断走 `truncate`。

数据挂 `msgMeta`（tui/data.go:158），与 `parents` / `res` / `docs` 同路，由 `loadMeta` 一次装好：话题侧用新的 `ThreadGists` 取回复数加最后一条回复，转发侧用 `ForwardGists` 取 `child_count`、前 `ForwardPreview` 条子消息，以及顶层子消息的源会话（`count(DISTINCT chat_id)` 加一次 `chats` 左连接）。预览封顶四条，所以查询规模仍与容器数同阶，不必分页。`ThreadGists` 的「最后一条」按 `(create_ms, message_position, id)` 倒序开窗，不用 `max(id)`——那是行被摄入的顺序，不是话题读起来的顺序。

帧里的嵌套 bundle 走 `ForwardLevels`：它没有 `forwarded_roots` 行可数，计数、预览与源会话都从已落地的子消息里来，而它按构造就是展开的——它的行就在库里。`loadForward` 打开一个非顶层的帧时也走这条路取**本层**的 gist，而不是沿用 bundle 的：后者会把外层的会话名挂到里层的帧上。

话题摘要**无条件**画在主流里：`ThreadReplyCounts` 不返回 0 回复的话题，计数缺失时写「No replies yet」而不是不画。这一行是进话题回复的入口，也是读者能看出 `Enter` 不会回复这条消息的唯一提示。

一条消息既是转发又是话题根时只画话题摘要：活的那个赢。转发摘要的可点性不丢——话题帧里装着根消息本身，在那里它是一行转发摘要，再点压下一层。层级与客户端一致（话题里包着转发），也保住了 `activate` 原有的 `ThreadID` 先判顺序。

上一段的推论：**转发摘要的画法与上下文无关，话题摘要只在主流里画**。话题帧里再写一遍「23 replies」是废话——回复就在它下面。

子消息没有 lark-cli 渲染过的 `content`——展开端点只回原始 body——所以映射时就地补一份：text 补它的话，image 补 `![Image](key)` 让图片走既有的 `splitImages` → `pictureRows`。**post 故意不补**：它的渲染是 markdown，只有 lark-cli 造得出，把压平的一行喂给 markdown 路径会把发送者的标点变成格式；它保留 `pendingText` 的暗色替身，那正是实情——字在，格式不在。卡片、通话、附件、表情包在读渲染之前就从 body 认出自己，不需要这一步。

子消息不可回复、不可加表情、不可撤回：`startInsert`、`openPicker`/`toggleReaction`、`askRecall` 在焦点落在转发帧时提前给提示。少了这道闸，`e` 会把一个表情贴到另一个会话的消息上——一个读者看不见的地方。

三处配套改动：

- `bodyRows`（tui/rows.go:588）对 `merge_forward` 直接返回 `nil`——卡片就是正文。这一行终结 2644 字符的字面倾泻。
- `solo`（tui/rows.go:473）加 `msg_type == "merge_forward"`：容器自带摘要，不能并进上一条的发送者块。
- `headLine`（tui/rows.go:548）去掉不带计数的 `⤷thread`——计数现在在摘要行上，头部再标一次是重复。

计数走 `plural`（tui/copy.go:214），所以一条回复写 `1 reply`、多条写 `N replies`。

摘要行的未读圆点自己设 `lead.mark`，不走 `leadFor`：`leads.first`（tui/rows.go:319）只落在消息的第一行上，而摘要行排在 `headLine` 与 `quoteRow` 之后，`used` 早已为真。字形与颜色沿用 `stAccent.Render("●")`，读者不必学第二个记号。

## 读态与未读

折叠改动读态的三处，方向各不相同，先写清楚免得改错地方。

未读计数与 `@` 徽章都走 `unreadCounted`（store/chats.go:270），而它经 `unreadBadge` 已含 `message_position >= 0`（store/resources.go:244）——话题回复今天就不计数、不点亮徽章，折叠不改变这一点。

### 清除

受影响的是 `MarkChatRead`（store/resources.go）。它故意用更宽的 `stillUnread`，注释写着理由：页面把这些回复摆在读者面前了。折叠后该理由失效，后果是**把读者看不见的回复标成已读**——是过度标记，不是标不掉。

`MarkChatRead` 因此收窄到 `unreadBadge`，`MarkThreadRead(ctx, threadID, now)` 承接话题那半。集合不再是徽标集的超集，而是与它相等。thread id 全局唯一且有自己的部分索引，所以不必再传会话。

`MarkThreadRead` 自己仍要是超集：它清该话题下**全部**未读回复，静音的也清。静音只决定要不要提醒，不决定读没读；漏掉它们会让一条静音回复把摘要行永久点着。

触发在话题帧每一次落地——`threadLoadedMsg` 的那一臂，它本就只在可见帧是该话题时才跑，所以被压住的帧自动不结算。不另设门：已读的话题上这条 UPDATE 一行都不匹配，安静的心跳什么都不写，`data_rev` 触发器也不响。不看滚动位置，也不要求终端持有焦点——话题是一屏的东西，读者把它调到最上面就是在读它。

`applyOutbox`（tui/outbox.go）靠 `it.chatID == m.chatID` 把待发的话题内消息放进主流，折叠后它会闪一下再消失，所以加 `&& !it.inThread`。

`threadLoadedMsg` 那一臂原本只在终端失焦时 `markDots`，理由是「会话页面也带着这些回复」。折叠后不成立，守卫去掉：这个面板是它们的标记唯一能亮的地方。它排在结算之前，读的正是结算即将清掉的那批标志。

`tui/readgate.go` 的 `readKey` 谓词跟着取 `unreadBadge`。它注释里关于「窄化会让只有回复未读的会话永远结算不掉」的警告因折叠而自动解除——回复不在页面上，也就没有要重画的标记。

`docs/silence/TECH.md` 与 `docs/read-sync/TECH.md` 都按名引用过这两个集合，跟着改。

### 参与

摘要行的圆点与会话列表里的话题行共用一条判据：这条话题里有我的一句话（根消息算我的），或者它的某条回复 `@` 了我。

```sql
-- A stake is either turn taken or name called: any row of mine in the thread,
-- the root included, or a mention of me on any of its messages.
EXISTS (SELECT 1 FROM messages t WHERE t.thread_id <> '' AND t.thread_id = x.thread_id
          AND t.deleted = 0 AND (t.sender_id = ? OR <namesPerson("t")>))
```

`thread_id <> ''` 要写出来，哪怕 join 已经蕴含它：`messages_thread` 是建在这个谓词上的部分索引，而计划器不会自己把条件跨相关子查询带过去。少了它，每条未读回复都要全表扫一遍 `messages`——`TestListThreadFeed_WalksTheThreadIndex` 就是锁这个的。

`namesSelf` 原本钉死在别名 `m` 上，改成 `namesPerson(alias)` 构造器；`threadStakeOn(alias)` 同理，因为它在两个查询里挂在不同的别名下。

判据不新增查询，挂在两条已有的路上：

- **摘要行的圆点**走 `ThreadGists(ctx, threadIDs, self)` 新增的 `Waiting`：未读、未静音、且我有份。静音那条照 `unreadCounted` 的规矩不点亮，但 `MarkThreadRead` 仍然收它——否则一条静音回复会把这行永远点着。
- **会话列表里的话题行**走 `store.ListThreadFeed`，与会话列表在同一次刷新里取回：话题在那里是自己的一行，不是会话行上的一个字段，所以它是自己的一趟查询而不是 `ListChats` 的又一个聚合。取值与版式见 [chats-list](../chats-list/PRD.md) 的「thread 行」。

圆点由读态派生，不进 `m.dots`：`clearDotsAtCursor` 收的是光标走过的那一块，而根消息早就读过了；把它并进去，光标扫过根就会抹掉读者没看过的回复。

### 会话列表里的话题行

话题在列表里是自己的一行，由 `listRows`（tui/listrow.go）把 `ListThreadFeed` 的结果与会话按时间归并出来，`renderThreadRow`（tui/threadrow.go）画它。

头像列画的是客户端自己的话题标记，托着该会话的头像：`threadmark.png` 是客户端 `resource.asar` 里的 `assets/img/80f6791e2e.png`，从它原本的白底上抠出来（一种青色压白底，逐像素的覆盖率从红通道反解），因为终端有自己的背景，一张白底圆盘会在头像列上凿个洞。合成在 `threadAvatar`（tui/threadavatar.go）：会话头像按 `threadBadge` 直接以徽标尺寸取一次，而不是从整格缩下来——缩两次的边缘会糊；再用 `fillRounded` 的透明填充在标记上凿一圈空隙，让终端背景充当客户端画的那圈白边。

`avatars` 的两个方法因此改吃 `listRow`，缓存的键从 `chat_id` 换成 `listRow.key()`：一个会话的两行要两个不同的计数，共用一个 image id 只会每一轮互相覆盖。

`nextUnread` / `waitingFor`（tui/nextunread.go:14）认它：它带的是一个计数，与会话行同类，`n` 的队列因此没有分级。静音继承所在会话——静音说的是「别拉我」，不是「别告诉我」，所以计数照画、画成灰的。

## 落地与跳转

`openHit`（tui/searchpanel.go:183）、`:mentions` 与 `jumpToQuoted`（tui/app.go:1153）都靠 `pendingSelect`（tui/app.go:189）落地，而 `app.go:539` 那段循环只在 `m.msgs` 里找。折叠后一条话题回复不在那儿，光标会静默停在页尾。

`pendingSelect` 从一个 id 变成 id 加它所在的层：

```go
// pendingJump is the message a landing is bound for, and the frame it lives
// in. A folded thread reply is in no chat page, so the thread opens over the
// chat and the cursor lands inside it.
type pendingJump struct{ id, thread string }
```

`jumpTo(x)` 是三个入口共用的判据：`MessagePosition < 0` 且有 `thread_id` 就走话题，其余落在页面上。

`openChatFrom` 之后：`thread` 为空照旧在 `m.msgs` 里找；不为空则 `pushRight(rightThread, thread)`，落位交给帧加载完成时的 `repinSelection`。整条话题随之结算——与 `jumped` 分支清掉 `clearBlockDots` 是同一条规矩：跳转落地按看见算。

填 `thread` 的地方就是命中本身：`searchHit.msg` 与 `MentionsOf` 的行都带着 `ThreadID` 与 `MessagePosition`，`MessagePosition < 0` 即是折叠的回复。

搜索命中转发**内部**的文字不走这条路。FTS 索引的是 `messages.content`，那棵字面树留在库里不动，所以命中的是 bundle 本身，落地就停在它的摘要行上，不自动压开转发帧。

## 键位

不新增 NORMAL 键，所以 `TestHelpEntries_DocumentEveryNormalKey`（tui/help_test.go:181，用 go/parser 扫 `onNormalKey` 的 case）自动满足。

| 键 | 改动 |
| --- | --- |
| `Enter` | `activate`（tui/app.go:1856）保持 `ThreadID` 先判——一条被开了话题的转发，光标下更活的那个是话题；`ThreadID` 为空时加一臂 `MsgType == "merge_forward"` |
| `t` | `toggleThread` 改为 `toggleRight`，三种容器都认：话题、转发，再落到光标所在的回复树 |
| `Esc` | 焦点在右栏时弹一帧 |

`r` / `R` / `e` / `f` / `y` 系列不动：容器是本会话的一条真实消息。

转发帧里的子消息继承右栏既有的键。`yy` / `yr` / `yc` 复制的是源消息真实的 id、raw json 与正文——这正是「子消息不是副本」的价值。`o` 的链接与附件照开，最后一行的「在飞书里打开这条消息」也保留：它指向源会话，开得了是好事，开不了是客户端的事，larkim 不替它判断。

`Enter` 不认回复树：一条普通消息的回复落在主流它自己旁边，把最常用的那个键从「回复」改成「打开」换不回任何东西。

`helpEntries` 改 `Enter`、`t`、`Esc` 三行的描述，MOUSE 段加两行说明摘要行可点、面板会叠。

## 测试

白盒，`larkcli.Fake` 加 `Bundles map[string][]RawForwarded` 与 `BundleErr map[string]error`（后者照 `ListErr` 的先例；`Forwarded` 那个名字已经归发出去的转发）。store 测试开真 SQLite 临时库。

### 数据与同步

| 用例 | 断言 |
| --- | --- |
| `TestSaveForwarded_KeepsAChildOutOfTheMessagesTable` | 子消息 id 已作为真实行存在时，那行的 `message_position` 纹丝不动——本方案的回归锁 |
| `TestSaveForwarded_TheSameMessageAtTwoDepthsKeepsBothRows` | 三列主键 |
| `TestToForwarded_TellsTheContainerFromItsChildrenByID` | 不靠 `message_position` 缺失认容器 |
| `TestExpandForwards_GroupsChildrenByUpperMessageID` | 扁平 items 还原成树 |
| `TestExpandForwards_ANestedBundleKeepsItsOwnChildren` | 嵌套层不塌进父层 |
| `TestExpandForwards_CountsOnlyTheTopLevelChildren` | `child_count` 的口径 |
| `TestExpandForwards_ARefusedBundleIsStampedAndNotRetried` | 永久失败不复问 |
| `TestForwardRootsDue_TakesTheNewestBundlesFirst` | 队列顺序 |
| `TestExpandForwards_RegistersAChildAttachmentUnderTheBundle` | 子消息附件挂在 bundle 上 |
| `TestMigrate_SeedsEveryStoredBundleIntoTheQueue` | 回填播种 |

### 右栏

| 用例 | 断言 |
| --- | --- |
| `TestPushRight_AForwardOpenedInsideAThreadKeepsTheThreadUnderIt` | 叠而不换 |
| `TestOpenRight_AThreadOpenedFromTheChatPaneReplacesTheColumn` | 兄弟不入栈 |
| `TestPushRight_PressingTheSameSummaryTwiceStacksOneFrame` | 栈顶去重 |
| `TestPopRight_EscUncoversTheFrameBeneath` | 逐层退 |
| `TestPopRight_ASuspendedFrameComesBackOnItsOwnCursor` | `sel` / `top` 复位 |
| `TestPopRight_EscOnTheLastFrameClosesTheColumn` | 退到底即关 |
| `TestEsc_OutsideTheRightPaneLeavesTheStackAlone` | 焦点不在右栏时不碰栈 |
| `TestToggleInfo_ResetsTheStackToOneFrame` | 根视图清栈 |
| `TestOpenThreadID_NamesTheThreadUnderAnOpenForward` | 心跳仍拉下面那层的回复 |
| `TestFocusMessages_OnAFoldedLayoutEmptiesTheWholeStack` | 窄屏离开清空 |

`tui/rightwheel_test.go` 三例与 `tui/info_test.go:97` 必须**原样通过**——它们是「可见帧摊开」这个取舍是否成立的判据。

### 渲染与点击

| 用例 | 断言 |
| --- | --- |
| `TestRenderRows_AForwardedBundleNeverPrintsItsTags` | 渲染结果不含 `<forwarded_messages>` 与 ISO 时间戳 |
| `TestRenderRows_AForwardCardIsBoundedHoweverBigTheBundleIs` | 无论几条子消息，卡片都是标题加至多四行预览加省略号 |
| `TestRenderRows_AForwardedBundleShowsItsFirstChild` | 转发取头 |
| `TestRenderRows_AThreadRootShowsItsLastReply` | 话题取尾 |
| `TestRenderRows_AThreadRootWithNoReplyStillGetsItsLine` | 0 回复也有入口 |
| `TestRenderRows_AnUnexpandedBundleNamesNoCount` | 未展开不写数字 |
| `TestRenderRows_ARefusedBundleSaysSo` | 被拒的说得出口 |
| `TestSelectedZones_LeavesTheSummaryLineToTheKeyboardsOwnKeys` | `o` 看不见摘要行 |
| `TestForwardTitle_NamesTheConversationTheWayTheClientDoes` | 群 / 单聊 / 自己的会话 / 无从命名，四种标题 |
| `TestRightTitle_NamesAForwardFrameAfterTheCardThatOpenedIt` | 帧的标题就是卡片的标题 |
| `TestLoadForward_ANestedLevelListsItsOwnChildren` | 一帧一层 |
| `TestLoadForward_APictureChildGetsOnlyItsOwnOfTheBundlesResources` | 资源按 key 分派回子消息 |
| `TestForwardedRow_AnImageChildPlacesItsPicture` | 图片走图片路径 |
| `TestForwardedRow_APostKeepsTheDimStandIn` | post 不喂给 markdown |
| `TestOnForwardLoaded_AnUnexpandedBundleIsAskedForNow` | 即时展开 |
| `TestOnForwardLoaded_WithoutTheSyncLockItSaysToWait` | 没锁就说等 |
| `TestForwardFrame_AChildCannotBeAnswered` | 子消息只读 |
| `TestRenderRows_AForwardedThreadRootShowsTheThreadInTheChatAndTheForwardInTheFrame` | 双重容器的分工 |
| `TestSummaryRow_KeepsTheCountWhenTheGistMustBeCut` | 窄面板下数量优先于内容 |
| `TestRenderRows_ASummaryLineCarriesAnOpenZone` | 整行一个 zone，其余行无 zone |
| `TestOnClick_ASummaryLineOpensTheContainerInTheRightPane` | 消息面板侧 |
| `TestOnClick_ASummaryPressedInsideTheRightPanePushesAFrame` | 右栏侧，偏移不同、结果相同 |
| `TestOnClick_ASummaryPressFiresOnTheFirstClick` | 单击即开，且光标未移动 |

### 折叠、读态与落地

| 用例 | 断言 |
| --- | --- |
| `TestMessageQuery_FoldsThreadRepliesOutOfTheChatFlow` | 回复离开主流 |
| `TestThreadGists_CountsTheRepliesAndTakesTheNewest` | 话题取尾，撤回的不算 |
| `TestJumpTo_AFoldedReplyIsReachedThroughItsThread` | 落地判据 |
| `TestMessagesLoaded_AHitOnThePageItselfLeavesTheColumnAlone` | 页面上的命中不开栏 |
| `TestRenderRows_AThreadSummaryIsNotDrawnInsideItsOwnPane` | 帧里不重复计数 |
| `TestRenderRows_AThreadSummaryCarriesAnOpenZone` | 话题摘要可点 |
| `TestMarkThreadRead_SettlesOnlyItsOwnReplies` | 只收自己那条话题，静音的也收 |
| `TestMarkChatRead_LeavesThreadRepliesUnread` | 打开会话不再清回复 |
| `TestMarkThreadRead_SettlesOnlyItsOwnReplies` | 打开话题才清 |
| `TestMarkThreadRead_SettlesASilencedReplyToo` | 静音回复也结算，否则圆点灭不掉 |
| `TestPushRight_AThreadFrameSettlesOnOpen` | 打开即结算，不看滚动 |
| `TestThreadLoaded_ANewReplySettlesWhileTheFrameIsOnTop` | 帧在栈顶时新回复随到随清 |
| `TestThreadLoaded_ABuriedFrameKeepsItsRepliesUnread` | 被压住不结算 |
| `TestThreadGists_WaitingOnlyForAThreadIAmIn` | 参与判据：说过话或被 @ |
| `TestThreadGists_ASilencedOrReadReplyIsNotWaiting` | 静音不点亮，结算后也不亮 |
| `TestListChats_MarksAChatWhoseOnlyUnreadIsAThreadIAmIn` | 会话列表记号，不计数不排序 |
| `TestListChats_WithoutAReaderNothingIsWaiting` | 空 needle 不匹配所有人 |
| `TestListChats_ThreadAggregateDrivesFromReadState` | 记号的代价与它标的东西同阶 |
| `TestRenderRows_AThreadSummaryCarriesTheUnreadDot` | 圆点落在摘要行的 lead 上 |
| `TestClearBlockDots_LeavesAThreadSummaryLit` | 光标扫过根不抹掉回复 |
| `TestRenderChatRow_MarksAChatWhoseOnlyUnreadIsAThread` | 会话列表记号 |
| `TestRenderChatRow_TheCountKeepsTheBadgeSlot` | 有计数时数字优先 |
| `TestRenderChatRow_AMutedChatStillSaysSo` | 静音照画，灰的 |
| `TestNextUnread_SkipsAChatWhoseOnlyUnreadIsAThread` | `n` 不认记号 |
| `TestOpenHit_AThreadReplyLandsInsideItsThreadFrame` | 搜索落地压帧并选中 |
| `TestOpenHit_AHitInsideABundleLandsOnTheBundle` | 转发内部命中不自动展开 |
| `TestThreadLoaded_LightsAMarkerForAReplyTheChatPaneNeverShowed` | 未读点要亮 |
| `TestApplyOutbox_PutsAThreadReplyInTheThreadPaneOnly` | 替换既有的 `…InBothPanes` |

### 回复树

| 用例 | 断言 |
| --- | --- |
| `TestReplyGists_CountsTheWholeSubtree` | 回复的回复也算，客户端的口径 |
| `TestReplyGists_NamesTheRootOfEveryMemberOfTheTree` | 树里每条都指向同一个根 |
| `TestReplyGists_LeavesOutAMessageInNoTree` | 没人回的消息不带这行 |
| `TestReplyGists_WalksThroughARecalledReplyWithoutCountingIt` | 撤回不计数，也不让下面的回复失去归属 |
| `TestReplyGists_TreatsAnUnsyncedParentAsTheRoot` | 本地看得见的最上面一条就是根 |
| `TestReplyTree_ListsTheRootThenItsAnswersInTheChatsOwnOrder` | 根在首行，旁边说的话不在里面 |
| `TestReplyTree_LeavesOutARecalledReplyAndKeepsWhatAnsweredIt` | 帧里不画撤回的那条 |
| `TestRenderRows_TheReplyCountSitsUnderTheBody` | 脚注不是头 |
| `TestRenderRows_AReplyDrawsNoCountOfItsOwn` | 只有根带这行 |
| `TestRenderRows_TheReplyCountIsNotDrawnInsideItsOwnPane` | 帧里不重复计数 |
| `TestRenderRows_AThreadRootDrawsItsThreadRatherThanItsReplies` | 话题赢 |
| `TestToggleRight_OpensTheDetailsFromAReplyToo` | 根翻页翻走了也开得出 |
| `TestActivate_EnterOnAnAnsweredMessageStillAnswersIt` | `Enter` 不被容器语义吃掉 |
| `TestActivate_EnterInsideTheDetailsPaneRepliesInTheMainFlow` | 不带 `reply_in_thread` |
| `TestOnReplyLoaded_FillsTheFrameAndNamesItAfterTheRoot` | 标题等树到了再填 |

## 分期

四期，每期独立可合入。

| 期 | 内容 | 合入后的状态 |
| --- | --- | --- |
| 1 | `forwarded_messages` + 同步展开 + 回填 + 附件注册 | 库里有数据，还没人读；SQL 可验 |
| 2 | 右栏改栈 + 合并转发折叠 + 转发帧 + 点击 | 主要收益落地 |
| 3 | 话题折叠 + 摘要 + 落地压帧 + `MarkChatRead` 收窄 + `MarkThreadRead` | 与客户端对齐 |
| 4 | 未读的可见性：参与判据、摘要圆点、列表记号、`markDots` 的焦点守卫 | 读者看得见哪条话题有新东西 |

四期走完，`docs/containers/` 就是这套行为的终态描述。

二需要一；三需要二；四需要三。

栈与转发帧同期落地，因为分开落地的那一版里栈是不可达的：从消息面板打开总是重置成一层，而唯一能埋下第二层的动作——在话题里打开一条转发——正是转发帧带来的。

读态的「清除」那半必须与折叠同期：折叠一落地，`MarkChatRead` 就在结算读者再也看不到的回复，而那正是验收 6 要挡的。留给下一期的是可见性——哪条话题里有新东西——它改错了只是看不见，不会丢掉「我还没读」。
