# Read sync — 技术方案

行为见 [PRD.md](PRD.md)。

## 结构

读挂在 `Model.Update`（`tui/app.go`）里，dispatch 之后的一道回合后检查上，而不是挂在页面到达的那个 case 上。页面到达只是「这一页来到读者眼前」的其中一种方式：滚回消息流末尾、关掉帮助层、把终端拉宽到不再折叠、切回窗口，同样是。它们都是普通的 `tea.Msg`，所以放在每条消息之后跑的一道检查一次覆盖全部，不必给每个滚动点各挂一个钩子。

```
任一 tea.Msg → dispatch
             → readKey 变了？
                 markChatRead       local_read_at，larkim 自己的徽标
                 clearFeishuBadge   applink，飞书客户端的红点
```

副作用层次按 ARCH.md 三：`local_read_at` 是必需的持久状态，先落；applink 是尽力而为的副作用，失败只提示；回执最终由既有的 read-status 轮询收口，不新增对账机制。

`markDots`（`tui/app.go`）与它共用 `pageShown`，但传的是**页面到达时**的 `tailed`，因为标记要回答「这条消息落进来的时候读者在不在看」，而不是这轮更新结束时在不在。

## 门控

两道门，各管一件事。

**`pageShown(tailed)`（`tui/readgate.go`）决定读不读。** 每一项都是「消息面板其实没在读者眼前」的一种：搜索/@我 面板借同一套 `msgRows`/`msgTop` 画自己的命中（`m.searching` 两者都置位），帮助层整屏盖住，终端窄到 `foldRight` 让右栏顶掉消息面板，尺寸低于 `minWidth`/`minHeight` 时 `View` 两栏都不画。视口本身的问题交给 `atTail` —— 滚轮把光标留在最新消息上也答不了它。

**`unreadWaiting(msgs)`（`tui/badgeclear.go`）决定投不投 applink。** 逐项复刻 `store.unreadBadge`——`is_read_remote = 0`、`local_read_at = 0`、`message_position >= 0`、未撤回。**它是 `markChatRead` 集合（`store.unreadInPane`）的子集，这是投出次数的上界所在**：这一页判为真的每一条，`markChatRead` 都在同一个 batch 里记为本地已读，下一页因此判为假。谓词放宽到那个集合之外，就会出现 `markChatRead` 永远收不掉的消息，每次 reload 都投一条 applink，直到会话被切走。

判据必须读页面查询时的状态：`markChatRead` 紧接着就把 `local_read_at` 写上，改用当前徽标数会被本次访问自己的写入打败。

不经 `Deps.Syncer`，与 `claimChatRefresh` 的门控相互独立：杠杆是桌面客户端，不是 data-dir 锁，跟着 daemon 跑的 TUI 同样要能清红点。

## readKey

`readKey` 取的是**页面上最新一条还欠着的消息**的 id，而不是最新一条消息的 id——未读有两种来法，只有这样两种都盖得住：

- 新消息带着自己的 id 进来；
- 读标记落在一条已经在页面上的消息身上。`read_state` 行由单独一趟写（`sync.checkReadStatus`，隔一个 chat beat 搭一次便车），所以把消息驮进来的那一页无事可做，而点亮徽标的那一页不带新 id。标记落在比最新消息更旧的那条上时同理。

谓词按 `store.unreadInPane` 取（含 thread 回复），也就是 `markChatRead` 会收掉的那一批。收窄到会话徽标自己那一套的话，一个只有 thread 回复未读的会话永远等不到一次 `markChatRead`，它的标记会在每次访问时重画。

**比对的是 `Model.readAt`（上次 takeRead 应答的那个 key），不是本轮更新之前的 key。** 后者会被 `key → "" → key` 的来回打败：读者在 `markChatRead` 的写入落地之前把视野挪开再挪回来，就会为同一条消息投第二次 applink。存下来还顺带改善了失败路径——`markChatRead` 出错时 key 不变，不会每次 reload 重投。

用 `msgsBase` 而不是 `msgs`：`msgs` 带着还在飞的发送，每次发送/回执都会让 key 抖一下。

## 不会自激

| 事件 | key | 触发 | applink |
|---|---|---|---|
| 视野在末尾，页面带着未读的 M10 | `C\|M10` | 是 | 是 |
| `markChatRead` 自己的 `data_rev` 引起的 reload | `""`（没有欠着的了） | 否 | 否 |
| 轮询按客户端回执写 `is_read_remote = 1` | `""` | 否 | 否 |

第二行就停了。没有冷却窗口：投出次数由未读消息条数决定而非 reload 次数，每条新消息本来就该对应一次清理——客户端的红点随每条消息重新亮起。加一层节流只会让落在窗口内的消息被 `markChatRead` 静静收掉、applink 再也不投。

## 注入缝

`Deps.OpenURL func(url string, background bool) error`（`tui/data.go`）。`New` 在其为 nil 时填 `openURL`，测试替换为 recorder，`open` 不在测试里跑。

`background` 分开两个调用点：`clearFeishuBadge` 传 true（`open -g`，焦点留在终端），`o` 键的 `openInFeishu` 传 false（用户要去飞书）。

`openURL` 用 `Run()` 而非 `Start()`：自动路径一天要跑很多次，不 reap 会攒僵尸进程。

## 数据

无 schema 变更、无 migration。`local_read_at` 不因 applink 能翻回执而退休：徽标要立刻落（回执有往返延迟）、回执过 7 天视野归 NULL、applink 可能静默失败。

## 测试

白盒，`Deps.OpenURL` 注入 recorder。`tui/badgeclear_test.go` 管 applink 这一侧：

| 用例 | 断言 |
|---|---|
| `TestUpdate_OpeningAChatWithUnreadClearsTheFeishuBadge` | 整条路径：一次访问投出一条后台 applink，URL 不带 `position` |
| `TestTakeRead_SendsNoApplinkWhenNothingWasWaiting` | 全部已读的页面不投 |
| `TestTakeRead_SendsNoApplinkForAPageAlreadyReadHere` | `local_read_at` 非零的 reload 不投 |
| `TestTakeRead_ClearsAgainForAMessageLandingInTheOpenChat` | 清红点不是一次性的 |
| `TestUpdate_AMessageLandingInTheOpenChatClearsTheBadgeAgain` | 整条路径：开着的会话收到新消息后再投一次 |
| `TestTakeRead_IgnoresUnreadTheChatBadgeLeavesOut` | thread 回复与已撤回消息不构成投出理由 |
| `TestTakeRead_ClearsBadgesWithoutASyncer` | `Syncer` 为 nil 照投 |
| `TestOpenInFeishu_TakesTheScreen` | `o` 键走同一条缝且不带 `-g` |

`tui/readgate_test.go` 管门控。`scrolledBack` 造一个页面高过面板、已读过、视野被滚回历史的会话：

| 用例 | 断言 |
|---|---|
| `TestUpdate_AMessageLandingBelowTheFoldLeavesTheChatUnread` | 视野外落进来的：未读数留着、不投 applink、带未读标记 |
| `TestUpdate_ScrollingBackToTheTailTakesWhatWasWaiting` | 滚回末尾一次落清，恰好一条后台 applink |
| `TestUpdate_ScrollingWithinTheHistoryTakesNothing` | 在历史里上下挪但没到末尾，什么都不落 |
| `TestUpdate_LeavingTheTailAndComingBackFiresOneApplink` | 视野来回离开末尾只投一次——`readAt` 的存在理由 |
| `TestUpdate_TheReadFlagLandingLateStillTakesTheChatRead` | 读标记晚于消息到达，照样落 |
| `TestUpdate_AReadFlagOnAnOlderMessageStillTakesTheChatRead` | 标记落在比最新消息更旧的那条上——`readKey` 取最新「欠着的」而非最新消息的理由 |
| `TestUpdate_ABlurredTerminalLeavesTheChatUnread` | 失焦时不落，`tea.FocusMsg` 回来才落 |
| `TestUpdate_TheHelpOverlayLeavesTheChatUnread` | 帮助层盖住时不落，关掉才落 |
| `TestUpdate_AFoldedAwayMessagePaneLeavesTheChatUnread` | `foldRight` 顶掉消息面板时不落，拉宽才落 |
| `TestUpdate_AJumpIntoHistoryLeavesWhatIsBelowItUnread` | 跳到历史中间的命中不落，滚到末尾才落 |
| `TestReadKey_IsEmptyWhileTheSearchPanelIsOpen` | 搜索/@我 面板开着时 `readKey` 为 `""` |
