# Read sync — 技术方案

行为见 [PRD.md](PRD.md)。

## 结构

清红点挂在 `takeRead`（`tui/app.go:497`）——既有的「读者把这一页放在眼前了」的唯一语义点。它在每次页面到达时跑，reload 也跑，所以「进入会话」和「开着的会话里来了新未读」共用一条路径，不需要第二个触发器。

```
页面到达 → markDots        保留未读标记（本次访问的写入不能把它抹掉）
         → takeRead
             markConsumed   CLI 游标
             markChatRead   local_read_at，larkim 自己的徽标
             clearFeishuBadge   applink，飞书客户端的红点
```

三个副作用的层次按 ARCH.md 三：`local_read_at` 是必需的持久状态，先落；applink 是尽力而为的副作用，失败只提示；回执最终由既有的 read-status 轮询收口，不新增对账机制。

## 门控

唯一的条件是 `unreadWaiting(msgs)`（`tui/badgeclear.go`）：这一页里有会话徽标算数的消息。

谓词逐项复刻 `store.unreadBadge`——`is_read_remote = 0`、`local_read_at = 0`、`message_position >= 0`、未撤回。**逐项对齐是投出次数的上界所在**：`markChatRead` 在同一个 batch 里把这批消息记为本地已读，下一页因此判为假；谓词放宽一项，就会出现 `markChatRead` 永远收不掉的消息，chat 消息流里混着的 thread 回复（`ListMessages` 不过滤负 position）会让每次 reload 都投一条 applink，直到会话被切走。

判据必须读页面查询时的状态：`markChatRead` 紧接着就把 `local_read_at` 写上，改用当前徽标数会被本次访问自己的写入打败。

没有冷却窗口。投出次数由未读消息条数决定而非 reload 次数，每条新消息本来就该对应一次清理——客户端的红点随每条消息重新亮起。加一层节流只会让落在窗口内的消息被 `markChatRead` 静静收掉、applink 再也不投。

不经 `Deps.Syncer`，与 `claimReadRefresh` 的门控相互独立：杠杆是桌面客户端，不是 data-dir 锁，跟着 daemon 跑的 TUI 同样要能清红点。

不会自激：applink → 客户端上报 → 轮询写 `is_read_remote = 1` → `data_rev` → TUI reload → 谓词为假 → 停。

## 注入缝

`Deps.OpenURL func(url string, background bool) error`（`tui/data.go`）。`New` 在其为 nil 时填 `openURL`，测试替换为 recorder，`open` 不在测试里跑。

`background` 分开两个调用点：`clearFeishuBadge` 传 true（`open -g`，焦点留在终端），`o` 键的 `openInFeishu` 传 false（用户要去飞书）。

`openURL` 用 `Run()` 而非 `Start()`：自动路径一天要跑很多次，不 reap 会攒僵尸进程。

## 数据

无 schema 变更、无 migration。`local_read_at` 不因 applink 能翻回执而退休：徽标要立刻落（回执有往返延迟）、回执过 7 天视野归 NULL、applink 可能静默失败。

## 测试

`tui/badgeclear_test.go`，白盒，`Deps.OpenURL` 注入 recorder：

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
