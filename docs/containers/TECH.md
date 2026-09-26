# Containers — 技术方案

行为见 [PRD.md](PRD.md)。

## 两种容器不同构

这是整份方案的前提，也是唯一一个不能照直觉走的地方。

话题回复取自 `GET /im/v1/messages?container_id_type=thread`：

```
id=om_r1  pos=-3  thread=omt_1  parent=om_root  root=om_root  chat=oc_a(本会话)
```

它是本会话的新消息：自己的 id、负位置哨兵、三个归属字段。它本来就在 `messages` 里，`pullChat`（sync/syncer.go:823）和开着话题时的心跳（sync/focus.go:29）都在拉它。

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

嵌套转发不在面板内缩进渲染，而是每层一帧。一个转发帧只装 `upper_message_id` 指向本帧的那些子消息；其中 `msg_type = merge_forward` 的画成摘要行，点它压下一帧。

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

`docs/SCHEMA.md` 增一节，并在所有权那段写明两张表都由守着 `daemon.lock` 的进程写。

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

读者打开一个尚未展开的转发时，按 `LaneInteractive` 即时取一次——那条 lane 就是为人留的。帧先压上、画一行「正在展开…」，数据到了就地填充。但只有持 `daemon.lock` 的进程能写，所以 `Syncer` 为 nil 时给提示而非静默失败，照 `jumpToQuoted`（tui/app.go:1182）「that message has yet to be synced」的先例。

API 拒绝（读者已退出源会话，或转发过旧）时按 `*larkcli.Error` 的 `IsPermanent()` 分流：永久失败记 `last_error` 并盖 `fetched_at`，不再重试——转发是冻结的，再问一次答案不会变；临时失败累加 `attempts` 并退避。摘要行随之画成无法展开，点击给提示而不是开空帧。

### 子消息的附件

子消息的 `content_raw` 自带 `file_key`，`ExtractResources` 原样抠得出来——只是要注册到 **bundle** 的 id 上。资源端点按「这个 key 在这条消息里吗」判权，而子消息的 id 在它自己的会话里；拿子消息的 id 去问，飞书答 `234003 File not in msg.`，拿 bundle 的 id 去问就给字节，图片与非图片都一样。`ExtractRendered` 把渲染串里的 `img_…` 挂在 bundle 上，正是同一条理由。

两条路注册的是同一批 key，`AddPendingResources` 按 key 去重，所以一张图只有一行。

展开渲染时把 bundle 的资源按 `FileKey` 分派回各子消息，否则一张图都画不出来。

## 渲染

容器的正文是**恰好一行**，由 `tui/summary.go` 的两个构造器产出，各返回单个 `msgRow`：

```go
func threadSummary(x store.Message, idx int, st msgStyle, g *leads) (msgRow, bool)
func forwardSummary(x store.Message, idx int, st msgStyle, g *leads) (msgRow, bool)
```

返回单行而非 `[]msgRow`，签名与 `quoteRow`（tui/rows.go:485）一致——它也是「一条消息附带的一行，整行一个 zone」，连 `bool` 的含义（这行该不该画）都相同。

行的构成是「数量 · 代表内容」，宽度分配照 `reactionChip`（tui/rows.go:725）的既有取舍：**数量必须活下来，内容按剩余宽度截断**。

```
⤷ 23 条回复 · 李四: 1234
⤷ 还没有回复
[合并转发] 5 条 · 张三: hey
[合并转发]
[合并转发] · 无法展开
```

代表的那一条，话题取尾、转发取头。`gist` 复用 `replyGist`（tui/replybar.go:183）的降级链，发送者名走 `displaySender`（tui/rows.go:513），截断走 `truncate`。

未展开的 bundle 只画 `[合并转发]`。不从 `content` 数 `[时间] 姓名:` 的行数补一个数字：那棵字面树含全部嵌套层，与 `child_count` 的顶层口径不同，补出来的数会在展开后当着读者的面跳。

数据挂 `msgMeta`（tui/data.go:158），与 `parents` / `res` / `docs` 同路，由 `loadMeta` 一次装好：话题侧用既有的 `ThreadReplyCounts`（store/messages.go:418）加最后一条回复，转发侧用新的 `ForwardGists` 取第一条子消息加 `child_count`。只取一条，所以两侧的查询都是每个容器一行，不必分页。

帧里的嵌套 bundle 走 `ForwardLevels`：它没有 `forwarded_roots` 行可数，计数与首条都从已落地的子消息里来，而它按构造就是展开的——它的行就在库里。

话题摘要**无条件**画在主流里：`ThreadReplyCounts` 不返回 0 回复的话题，计数缺失时写「还没有回复」而不是不画。这一行是进话题回复的入口，也是读者能看出 `Enter` 不会回复这条消息的唯一提示。

一条消息既是转发又是话题根时只画话题摘要：活的那个赢。转发摘要的可点性不丢——话题帧里装着根消息本身，在那里它是一行转发摘要，再点压下一层。层级与客户端一致（话题里包着转发），也保住了 `activate` 原有的 `ThreadID` 先判顺序。

上一段的推论：**转发摘要的画法与上下文无关，话题摘要只在主流里画**。话题帧里再写一遍「23 条回复」是废话——回复就在它下面。

子消息没有 lark-cli 渲染过的 `content`——展开端点只回原始 body——所以映射时就地补一份：text 补它的话，image 补 `![Image](key)` 让图片走既有的 `splitImages` → `pictureRows`。**post 故意不补**：它的渲染是 markdown，只有 lark-cli 造得出，把压平的一行喂给 markdown 路径会把发送者的标点变成格式；它保留 `pendingText` 的暗色替身，那正是实情——字在，格式不在。卡片、通话、附件、表情包在读渲染之前就从 body 认出自己，不需要这一步。

子消息不可回复、不可加表情、不可撤回：`startInsert`、`openPicker`/`toggleReaction`、`askRecall` 在焦点落在转发帧时提前给提示。少了这道闸，`e` 会把一个表情贴到另一个会话的消息上——一个读者看不见的地方。

三处配套改动：

- `bodyRows`（tui/rows.go:588）对 `merge_forward` 直接返回 `nil`——摘要就是正文。这一行终结 2644 字符的字面倾泻。
- `solo`（tui/rows.go:473）加 `msg_type == "merge_forward"`：容器自带摘要行，不能并进上一条的发送者块。
- `headLine`（tui/rows.go:548）去掉不带计数的 `⤷thread`——计数现在在摘要行上，头部再标一次是重复。

计数措辞直接写 `N 条回复`，不走 `plural`（tui/copy.go:214）——那个渲染英文复数，是给 agent 导出用的，中文没有复数。

摘要行的未读圆点自己设 `lead.mark`，不走 `leadFor`：`leads.first`（tui/rows.go:319）只落在消息的第一行上，而摘要行排在 `headLine` 与 `quoteRow` 之后，`used` 早已为真。字形与颜色沿用 `stAccent.Render("●")`，读者不必学第二个记号。

## 读态与未读

折叠改动读态的三处，方向各不相同，先写清楚免得改错地方。

未读计数与 `@` 徽章都走 `unreadCounted`（store/chats.go:270），而它经 `unreadBadge` 已含 `message_position >= 0`（store/resources.go:244）——话题回复今天就不计数、不点亮徽章，折叠不改变这一点。

### 清除

受影响的是 `MarkChatRead`（store/resources.go:261）。它故意用更宽的 `unreadInPane`，注释写着理由：页面把这些回复摆在读者面前了。折叠后该理由失效，后果是**把读者看不见的回复标成已读**——是过度标记，不是标不掉。

改法：`MarkChatRead` 收窄到 `unreadBadge`，新增 `MarkThreadRead(ctx, chatID, threadID, now)` 承接话题那半。store/resources.go:253 那段注释要**反过来写**——集合不再需要是徽标集的超集，而是要与它相等。

`MarkThreadRead` 自己仍要是超集：它清该话题下**全部**未读回复，静音的也清。静音只决定要不要提醒，不决定读没读；漏掉它们会让一条静音回复把摘要行的圆点永久点着。

触发点两个：`pushRight` 压开一个话题帧时；以及此后该帧留在栈顶期间，每次 `threadLoadedMsg` 带来新回复时。不看滚动位置，也不要求终端持有焦点——话题是一屏的东西，读者把它调到最上面就是在读它。被别的帧压住时不结算：它不在屏幕上。

连带两处代码改动，不只是测试：

- `tui/outbox.go:99`：`applyOutbox` 靠 `it.chatID == m.chatID` 把待发的话题内消息放进主流，折叠后它会闪一下再消失。加 `&& !it.inThread`。
- `tui/app.go:668`：点亮未读标记的 `if !m.focused` 守卫要去掉。它的理由是「会话页面也带着这些回复」，折叠后不成立；不去掉的话话题面板一个未读点都不会亮。

`tui/readgate.go:19` 镜像 `unreadInPane`，其注释里关于「窄化会让只有回复未读的会话永远结算不掉」的警告，正因折叠而自动解除——回复不在页面上，也就没有要重画的标记。注释改写，不要删。

### 参与

摘要行的圆点与会话列表的记号共用一条判据：这条话题里有我的一句话（根消息算我的），或者它的某条回复 `@` 了我。

```sql
-- A stake is either turn taken or name called: any row of mine in the thread,
-- the root included, or a mention of me on any of its messages. Silenced
-- replies light nothing, on the rule unreadCounted already states — but
-- MarkThreadRead still settles them, or one would keep the dot lit for good.
EXISTS (SELECT 1 FROM messages t
          WHERE t.chat_id = m.chat_id AND t.thread_id = m.thread_id
            AND t.deleted = 0 AND (t.sender_id = ? OR <namesSelf on t>))
```

`namesSelf`（store/chats.go:285）钉在别名 `m` 上，这里要一个同式的 `t` 版本；把它改成接受别名的构造器比复制一份好。

两个查询，两种形状：

```go
// UnreadThreadsIn names the threads of one chat holding an unread reply the
// reader has a stake in, for the dot on the summary line.
func (s *Store) UnreadThreadsIn(ctx context.Context, chatID, self string) (map[string]bool, error)

// ChatsWithUnreadThreads is the same rule across every chat, for the list's
// marker. One query for the whole pane: the alternative is a subquery per row.
func (s *Store) ChatsWithUnreadThreads(ctx context.Context, self string) (map[string]bool, error)
```

前者进 `msgMeta`，与 `ThreadReplyCounts` 同一趟；后者随 `m.unread` 一起加载。

### 会话列表的记号

`renderChatRow`（tui/chatrow.go:398）右侧今天是 `badge + " " + 时间`。记号占 `badge` 位：有计数未读时让位给数字（数字更要紧），没有时画 `stAccent.Render("⤷")`，与摘要行同一个符号，读者一眼认得出是话题。静音会话照画，但走 `counterStyle`（tui/chatrow.go:426）的灰——静音说的是「别拉我」，不是「别告诉我」。

`nextUnread` / `waitingFor`（tui/nextunread.go:14）不认这个记号。`n` 的含义是清队列，队列就是徽章那一份；分两级之后它不再是一个能学会的键。

## 落地与跳转

`openHit`（tui/searchpanel.go:183）、`:mentions` 与 `jumpToQuoted`（tui/app.go:1153）都靠 `pendingSelect`（tui/app.go:189）落地，而 `app.go:539` 那段循环只在 `m.msgs` 里找。折叠后一条话题回复不在那儿，光标会静默停在页尾。

`pendingSelect` 从一个 id 变成 id 加它所在的层：

```go
// pendingSelect is the message a jump is bound for. thread names the frame it
// lives in, empty for the chat's own flow: a folded reply is not in m.msgs,
// and landing on the chat with nothing selected is the silent failure this
// field exists to make impossible.
pendingSelect struct{ id, thread string }
```

`openChatFrom` 之后：`thread` 为空照旧在 `m.msgs` 里找；不为空则 `pushRight(rightThread, thread)`，落位交给帧加载完成时的 `repinSelection`。整条话题随之结算——与 `jumped` 分支清掉 `clearBlockDots` 是同一条规矩：跳转落地按看见算。

填 `thread` 的地方就是命中本身：`searchHit.msg` 与 `MentionsOf` 的行都带着 `ThreadID` 与 `MessagePosition`，`MessagePosition < 0` 即是折叠的回复。

搜索命中转发**内部**的文字不走这条路。FTS 索引的是 `messages.content`，那棵字面树留在库里不动，所以命中的是 bundle 本身，落地就停在它的摘要行上，不自动压开转发帧。

## 键位

不新增 NORMAL 键，所以 `TestHelpEntries_DocumentEveryNormalKey`（tui/help_test.go:181，用 go/parser 扫 `onNormalKey` 的 case）自动满足。

| 键 | 改动 |
| --- | --- |
| `Enter` | `activate`（tui/app.go:1856）保持 `ThreadID` 先判——一条被开了话题的转发，光标下更活的那个是话题；`ThreadID` 为空时加一臂 `MsgType == "merge_forward"` |
| `t` | `toggleThread` 改为 `toggleRight`，两种容器都认 |
| `Esc` | 焦点在右栏时弹一帧 |

`r` / `R` / `e` / `f` / `y` 系列不动：容器是本会话的一条真实消息。

转发帧里的子消息继承右栏既有的键。`yy` / `yr` / `yc` 复制的是源消息真实的 id、raw json 与正文——这正是「子消息不是副本」的价值。`o` 的链接与附件照开，最后一行的「在飞书里打开这条消息」也保留：它指向源会话，开得了是好事，开不了是客户端的事，larkim 不替它判断。

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
| `TestRenderRows_AContainerTakesExactlyOneBodyRow` | 无论几条子消息、几条回复，正文都只有一行 |
| `TestRenderRows_AForwardedBundleShowsItsFirstChild` | 转发取头 |
| `TestRenderRows_AThreadRootShowsItsLastReply` | 话题取尾 |
| `TestRenderRows_AThreadRootWithNoReplyStillGetsItsLine` | 0 回复也有入口 |
| `TestRenderRows_AnUnexpandedBundleNamesNoCount` | 未展开不写数字 |
| `TestRenderRows_ARefusedBundleSaysSo` | 被拒的说得出口 |
| `TestSelectedZones_LeavesTheSummaryLineToTheKeyboardsOwnKeys` | `o` 看不见摘要行 |
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
| `TestMarkChatRead_LeavesThreadRepliesUnread` | 打开会话不再清回复 |
| `TestMarkThreadRead_SettlesOnlyItsOwnReplies` | 打开话题才清 |
| `TestMarkThreadRead_SettlesASilencedReplyToo` | 静音回复也结算，否则圆点灭不掉 |
| `TestPushRight_AThreadFrameSettlesOnOpen` | 打开即结算，不看滚动 |
| `TestThreadLoaded_ANewReplySettlesWhileTheFrameIsOnTop` | 帧在栈顶时新回复随到随清 |
| `TestThreadLoaded_ABuriedFrameKeepsItsRepliesUnread` | 被压住不结算 |
| `TestUnreadThreadsIn_NamesOnlyTheThreadsIAmIn` | 参与判据：说过话或被 @ |
| `TestUnreadThreadsIn_ASilencedReplyLightsNothing` | 静音不点亮 |
| `TestRenderRows_ASummaryLineCarriesTheUnreadDot` | 圆点落在摘要行的 lead 上 |
| `TestRenderChatRow_MarksAChatWhoseOnlyUnreadIsMyThread` | 会话列表记号 |
| `TestRenderChatRow_TheCountKeepsTheBadgeSlot` | 有计数时数字优先 |
| `TestNextUnread_SkipsAChatWhoseOnlyUnreadIsAThread` | `n` 不认记号 |
| `TestOpenHit_AThreadReplyLandsInsideItsThreadFrame` | 搜索落地压帧并选中 |
| `TestOpenHit_AHitInsideABundleLandsOnTheBundle` | 转发内部命中不自动展开 |
| `TestThreadLoaded_LightsAMarkerForAReplyTheChatPaneNeverShowed` | 未读点要亮 |
| `TestApplyOutbox_PutsAThreadReplyInTheThreadPaneOnly` | 替换既有的 `…InBothPanes` |

## 分期

四期，每期独立可合入。

| 期 | 内容 | 合入后的状态 |
| --- | --- | --- |
| 1 | `forwarded_messages` + 同步展开 + 回填 + 附件注册 | 库里有数据，还没人读；SQL 可验 |
| 2 | 右栏改栈 + 合并转发折叠 + 转发帧 + 点击 | 主要收益落地 |
| 3 | 话题折叠 + 摘要 + 落地压帧 | 与客户端对齐 |
| 4 | 读态与未读：`MarkChatRead` 收窄、`MarkThreadRead`、参与判据、摘要圆点、列表记号 | 未读语义收口 |

二需要一；三需要二；四需要三。

栈与转发帧同期落地，因为分开落地的那一版里栈是不可达的：从消息面板打开总是重置成一层，而唯一能埋下第二层的动作——在话题里打开一条转发——正是转发帧带来的。读态单独一期，因为它是全仓最容易改错的一块，也是唯一一个改错了会静默丢掉「我还没读」的地方，该有一个能单独回滚的提交。
