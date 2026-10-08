# Read sync — 技术方案

行为见 [PRD.md](PRD.md)。

## 结构

读挂在 `Model.Update`（`tui/app.go`）里，dispatch 之后的一道回合后检查上，而不是挂在页面到达的那个 case 上。页面到达只是「这一页来到读者眼前」的其中一种方式：滚回消息流末尾、关掉帮助层、把终端拉宽到不再折叠、切回窗口，同样是。它们都是普通的 `tea.Msg`，所以放在每条消息之后跑的一道检查一次覆盖全部，不必给每个滚动点各挂一个钩子。

```
任一 tea.Msg → dispatch
             → readKey 变了？
                 markChatRead       local_read_at，larkim 自己的徽标
                 clearFeishuBadge   markread.Clear → larkweb gateway
```

副作用层次按 ARCH.md 三：`local_read_at` 是必需的持久状态，先落；gateway POST 是尽力而为的副作用，失败只提示；回执最终由既有的 read-status 轮询收口，不新增对账机制。

`markDots`（`tui/app.go`）与它共用 `pageShown`，但传的是**页面到达时**的 `tailed`，因为标记要回答「这条消息落进来的时候读者在不在看」，而不是这轮更新结束时在不在。

## 门控

两道门，各管一件事。

**`pageShown(tailed)`（`tui/readgate.go`）决定读不读。** 每一项都是「消息面板其实没在读者眼前」的一种：搜索/Mentions 面板借同一套 `msgRows`/`msgTop` 画自己的命中（`m.searching` 两者都置位），Unread 面板借同一套画每个还欠着的会话（`m.feed` 非 nil；`m.chatID` 全程指着某个真实会话，但摆在读者眼前的是一页很多会话，光标停的那个并没有在被读——`Enter` 进去才是），帮助层整屏盖住，终端窄到 `foldRight` 让右栏顶掉消息面板，尺寸低于 `minWidth`/`minHeight` 时 `View` 两栏都不画。视口本身的问题交给 `atTail` —— 滚轮把光标留在最新消息上也答不了它。

**`unreadWaiting(msgs)`（`tui/badgeclear.go`）决定清不清 Feishu 红点。** 它与批量清理的集合**不同**，这是故意的：自动路径每次 reload 都跑，上界必须是「每条未读消息一次」，所以它绑在 `markChatRead` 的写上；批量清理是读者按一次才跑一次，上界是按下的次数，所以它绑在回执上。逐项复刻 `store.unreadBadge`——`is_read_remote = 0`、`local_read_at = 0`、`message_position >= 0`、未撤回。**它取的正是 `markChatRead` 的集合（`store.unreadBadge`），这是清红点次数的上界所在**：这一页判为真的每一条，`markChatRead` 都在同一个 batch 里记为本地已读，下一页因此判为假。谓词放宽到那个集合之外，就会出现 `markChatRead` 永远收不掉的消息，每次 reload 都清一次，直到会话被切走。

判据必须读页面查询时的状态：`markChatRead` 紧接着就把 `local_read_at` 写上，改用当前徽标数会被本次访问自己的写入打败。

## 清红点队列

所有清红点经 `clearQueue`（`tui/clearq.go`）排队，一条自续的 `tea.Tick` 链按 `clearPace`（`larkweb.Pace`）逐条 POST。read gate 一次压一个，批量清理一次压一批，两者共用这一条队列。

理由是写者收敛：一次 sweep 几十个 POST 不留间隔就是一个没测过频控的突发；串行 + 100ms 间隔是实测留出的余量。

- **查重**：同一个 chat id 还在队里就不重复压，只把它的水位抬到较大的那个；gateway 只需被告知一次，且要告知到最新的水位。
- **代次**：`gen` 让被清空的队列的迟到 tick 落地即丢——链一旦挂出去就撤不回来，能撤的只有它送达的那条消息。
- **顺序**：读者当前所在的会话排在最后。
- **失败**：逐条计数，批量清理收尾时报一次；单条不报——那是导航的副产物，不是读者请求打开的东西。

`tea.Tick` 的计时从构造时起，所以 tick 与 `ClearBadge` 同批挂出时，间隔量的是两次 POST 开始之间。

`larkim read-all` 在循环里同样 `time.Sleep(larkweb.Pace)`。

## 批量的两个谓词

两个谓词互不包含，各自回答一件事。

|  | 谓词 | 常量 |
|---|---|---|
| `ChatsWithUnread` 查要走哪些会话 | `is_read_remote = 0 AND deleted = 0 AND message_position >= 0` | `clientDot` |
| `MarkAllRead` 写本地已读 | `is_read_remote = 0 AND local_read_at = 0 AND deleted = 0` | `stillUnread` |

查集合里没有 `local_read_at`：红点是客户端的，larkim 在这边把消息记为已读从来没让它落下来。用本地事实去回答「客户端还亮不亮」，会让一次没落到客户端的 sweep 变成不可重复的——写已经把会话收掉了，再按一次也找不回来。

因此收敛点不是同一次写，而是**飞书的回执**：gateway 成功 → read-status 轮询问到它 → `is_read_remote` 翻 1 → 离开集合。没有回执的会话留在集合里，下一次按下重试，直到 7 天视野把 `is_read_remote` 刷成 NULL。

这就是 `ReadStatusProbes` 不再过滤 `local_read_at` 的理由。阶梯（`ReadStatusCandidates`）本来就不看它，所以探针买的是延迟：一个 tick，而不是最长 6 小时的退避步长——那段时间里 sweep 会一直走向红点早就落下的会话。

查在写之前跑只是顺序上的省事，不再是约束：写不会抹掉查所依据的证据。

thread 回复与已撤回不进查询集合：水位只收主消息流，thread 的 receipt 不翻，列进去就是每次都走。现在 `message_position >= 0` 与 `deleted = 0` 是唯二做这件排除的条件。本地已读照收——`MarkAllRead` 用的是更宽的 `stillUnread`。

不经 `Deps.Syncer` 与 data-dir 锁无关；收敛所依赖的那趟轮询不受此影响——没有 daemon 的 TUI 自己拿 `daemon.lock` 并内嵌跑 `Syncer.Run`（`cli/tui_cmd.go`），凡是在同步数据的配置都有一趟。

## readKey

`readKey` 取的是**页面上最新一条还欠着的消息**的 id，而不是最新一条消息的 id——未读有两种来法，只有这样两种都盖得住：

- 新消息带着自己的 id 进来；到达即存为未读的，标记与消息同一次写入；
- 其余的读标记落在一条已经在页面上的消息身上。这些 `read_state` 行由单独一趟写（`sync.checkReadStatus`，隔一个 chat beat 搭一次便车），所以把消息驮进来的那一页无事可做，而点亮徽标的那一页不带新 id。标记落在比最新消息更旧的那条上时同理。

谓词按 `store.unreadBadge` 取，也就是 `markChatRead` 会收掉的那一批。会话页把 thread 回复折进根消息那一行，页面上一条都没有，所以这里也数不到它们；它们由 `markThreadRead` 在话题面板打开时收掉。

**比对的是 `Model.readAt`（上次 takeRead 应答的那个 key），不是本轮更新之前的 key。** 后者会被 `key → "" → key` 的来回打败：读者在 `markChatRead` 的写入落地之前把视野挪开再挪回来，就会为同一条消息清第二次。存下来还顺带改善了失败路径——`markChatRead` 出错时 key 不变，不会每次 reload 重投。

用 `msgsBase` 而不是 `msgs`：`msgs` 带着还在飞的发送，每次发送/回执都会让 key 抖一下。

## 不会自激

| 事件 | key | 触发 | 清红点 |
|---|---|---|---|
| 视野在末尾，页面带着未读的 M10 | `C\|M10` | 是 | 是 |
| `markChatRead` 自己的 `data_rev` 引起的 reload | `""`（没有欠着的了） | 否 | 否 |
| 轮询按客户端回执写 `is_read_remote = 1` | `""` | 否 | 否 |

第二行就停了。没有冷却窗口：次数由未读消息条数决定而非 reload 次数，每条新消息本来就该对应一次清理——客户端的红点随每条消息重新亮起。加一层节流只会让落在窗口内的消息被 `markChatRead` 静静收掉、清红点再也不发。

## 注入缝

`Deps.OpenURL func(targets []string) error`（`tui/data.go`）。`New` 在其为 nil 时填 `applink.Open`，测试替换为 recorder。`o` 键与附件/链接区走这里，前置桌面客户端。

清红点另走一条缝：`Deps.ClearBadge markread.Clear`，由 `Deps.NewClearBadge` 在 `:set mark_read.browser` 时重建。`markread.New` 构造 gateway 杠杆；TUI 与 `larkim read-all` 共用。CLI 侧 `App.clearBadge`，测试注入 recorder。

包级变量 `openApplink`、`newClearBadge`、`clearPace`（`tui/app.go`）供 `TestMain` 替换，避免测试读 Keychain 或 POST 真 gateway。

## web gateway

`larkweb` 直连 `internal-api-lark-api.feishu.cn/im/gateway/`，body 是 protobuf 的 `Packet` 信封（`payloadType=2, cmd=3, payload=5, cid=6`），路由靠 `x-command` 头，认证只有 cookie（必须有 `session`），无 CSRF、无签名。cookie 由 kooky 每次调用时从 `mark_read.browser` 的 jar 读，只登记 Chromium 系，Safari 不进来，免得 Full Disk Access 的失败混进来。

`PutReadMessages`（cmd 40）的 `chatId` 是 web client 的**数字** id，传 `oc_` 会被 400 拒（`strconv.ParseInt`）。两套 id 之间没有任何字段或接口相连，`entities.Chat.openChatId` 在 inbox 回包里全为空。对法是消息：`feed.PullFeedCards`（cmd 1000，INBOX）给出每个会话最新一条消息的 `chatId`、`position`、`createTimeMs`，`store.ChatAt(create_ms, message_position)` 恰好命中一个本地会话时才建立映射。对不上的多是最新消息早于 backfill 视野的会话，本来就不会有 larkim 侧的未读。

Collapsed Chats 在 inbox 顶层只是一张 `type=BOX` 的卡片，折进去的会话不出现在顶层回包里；`LastMessages` 对每张 box 卡再以 `parentCardId` 拉一遍它自己的一层。

匹配键只用 `(create_ms, message_position)`，不加 sender：本地库里跨会话同毫秒的消息全是同一个人同时发往两个群，sender 相同，分开它们的是 position。

对上的 id 记进 `chats.web_chat_id`（migration `0043`），所以一个会话只需对上一次：之后它移进 Done、或 inbox 列出的最新消息还没同步下来，都照样能清。一次 inbox 拉取把能对上的会话全部记下，一次 sweep 通常不再拉。

没记过的会话先拉一次 inbox 去对；仍对不上就报错不发——猜错的代价是把别的会话记为已读。两次拉取之间至少隔 `relistAfter`（1 分钟），免得一次 sweep 里每个对不上的会话各拉一遍整份 inbox（实测 4 页、500+ 会话）。

数字 id 是飞书侧会话的内部主键，同一会话在所有探测里都是同一个值；没有观察到它会变，因此不为「记下的 id 失效」写回退。

清红点的 POST 带上该会话**最新一条仍欠着的消息**的 `position`（`store.ChatUnread`）。不带的话客户端停在它自己的未读分隔线上，积压深的会话那条线就在历史中间——正是 `pageShown` 判为「没读」的那种落点。带上的是库里真实存在的序号，不是一个越界的大数：越界值客户端不保证跳到末尾。

## 数据

`chats.web_chat_id`（见上）。`read_state_unread(is_read_remote, local_read_at, message_id)` 仍以 `is_read_remote` 领衔，`clientDot` 只约束首列，索引照样能驱动 read_state 一侧且仍然覆盖——拿得到 `message_id` 去 join，不必回表。代价只是选择性：命中的行从「当前有徽标的」变成「7 天视野内远端仍未读的」，而 `ExpireReadStatus` 是这个集合现在唯一的上界。

`local_read_at` 不因 gateway 能翻回执而退休：徽标要立刻落（回执有往返延迟）、回执过 7 天视野归 NULL、POST 可能静默失败。

## 测试

白盒，`Deps.ClearBadge` 由测试 recorder 注入；gateway 在 `larkweb` 里对 `httptest` 的假 gateway 测。`tui/badgeclear_test.go` 管自动清这一侧：

| 用例 | 断言 |
|---|---|
| `TestUpdate_OpeningAChatWithUnreadClearsTheFeishuBadge` | 整条路径：一次访问清一次，带 chat id |
| `TestTakeRead_ClearsNothingWhenNothingWasWaiting` | 全部已读的页面不清 |
| `TestTakeRead_ClearsNothingForAPageAlreadyReadHere` | `local_read_at` 非零的 reload 不清 |
| `TestTakeRead_ClearsAgainForAMessageLandingInTheOpenChat` | 清红点不是一次性的 |
| `TestUpdate_AMessageLandingInTheOpenChatClearsTheBadgeAgain` | 开着的会话收到新消息后再清一次 |
| `TestTakeRead_IgnoresUnreadTheChatBadgeLeavesOut` | thread 回复与已撤回消息不构成理由 |
| `TestOpenInFeishu_HandsTheChatLinkToTheDesktop` | `o` 键走 `OpenURL`，带 chat link |

`tui/markall_test.go` 管批量，`cli/readall_cmd_test.go` 管 CLI 入口。两边的 `ClearBadge` 都换成 recorder；TUI 侧用 `drain` 把队列走到底，它按 `clearDueMsg` 逐格推进而不等 tick 自己响。

`tui/readgate_test.go` 管门控。`scrolledBack` 造一个页面高过面板、已读过、视野被滚回历史的会话：

| 用例 | 断言 |
|---|---|
| `TestUpdate_AMessageLandingBelowTheFoldLeavesTheChatUnread` | 视野外落进来的：未读数留着、不清、带未读标记 |
| `TestUpdate_ScrollingBackToTheTailTakesWhatWasWaiting` | 滚回末尾一次落清，恰好一条 clear |
| `TestUpdate_ScrollingWithinTheHistoryTakesNothing` | 在历史里上下挪但没到末尾，什么都不落 |
| `TestUpdate_LeavingTheTailAndComingBackClearsOnce` | 视野来回离开末尾只清一次——`readAt` 的存在理由 |
| `TestUpdate_TheReadFlagLandingLateStillTakesTheChatRead` | 读标记晚于消息到达，照样落 |
| `TestUpdate_AReadFlagOnAnOlderMessageStillTakesTheChatRead` | 标记落在比最新消息更旧的那条上——`readKey` 取最新「欠着的」而非最新消息的理由 |
| `TestUpdate_ABlurredTerminalLeavesTheChatUnread` | 失焦时不落，`tea.FocusMsg` 回来才落 |
| `TestUpdate_TheHelpOverlayLeavesTheChatUnread` | 帮助层盖住时不落，关掉才落 |
| `TestUpdate_AFoldedAwayMessagePaneLeavesTheChatUnread` | `foldRight` 顶掉消息面板时不落，拉宽才落 |
| `TestUpdate_AJumpIntoHistoryLeavesWhatIsBelowItUnread` | 跳到历史中间的命中不落，滚到末尾才落 |
| `TestReadKey_IsEmptyWhileTheSearchPanelIsOpen` | 搜索/Mentions 面板开着时 `readKey` 为 `""` |
