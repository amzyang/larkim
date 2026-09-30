# Streaming card — 调研

以本人身份往会话发一张卡片，正文随生成过程逐段更新。目标场景是把 AI 面板（`ai.Stream`）的回答直接
流进会话，而不是先落进 composer 再整段发出。

有两条可行路线，2026-09-30 经 `lark-cli api` 在 robot p2p 与 self p2p 实测：

| | A. 整卡替换 | B. CardKit 流式 |
| --- | --- | --- |
| 身份 | 全程 user | bot 建卡与写入，user 发送 |
| 客户端效果 | 内容分段跳变 | 逐字打字机 |
| 权限 | 现有的 user 发消息 scope | 另需 bot 的 `cardkit:card:write` |
| 并发控制 | 无 `sequence`，靠串行保证顺序 | 服务端按 `sequence` 拒绝回退 |
| 单次写入 | 约 550–590 ms | 约 610–650 ms |
| 限制 | 14 天内、content ≤ 30 KB | 流式空闲 ≤ 11 分钟内被服务端关闭 |

建议先做 A：一个身份、一条接口、不引入 bot 与 CardKit，AI 回答本来就按段落涌出，
分段跳变在阅读上可以接受。B 作为后续的体验升级，管道设计与 A 共用。

## A. 整卡替换

1. user 发送 `msg_type: interactive`，content 直接是 2.0 卡片 JSON，正文一个 markdown 元素。
2. user 反复 `PATCH /open-apis/im/v1/messages/{message_id}`，content 为替换后的完整卡片 JSON，
   正文写**累积全文**。

实测约束：

- 串行 200 次更新同一条消息全部成功，每次约 550–590 ms，即自然节奏约 1.7 次/秒，没有触发频控，
  也没有碰到次数上限。
- 8 个请求并发打同一条消息，2 个被拒：`230020 This operation triggers the frequency limit,
  ext=Update the single messages too frequently`。频控按单条消息计。
- 没有 `sequence`，并发写的落地顺序不确定，终态可能停在较早的文本。写入必须按消息串行。
- 收件人一侧不显示「已编辑」标记。
- 接口文档（lark-cli catalog 的 `messages.patch`）写明：消息须在 14 天内发出，content 不超过 30 KB。
  长回答要在 30 KB 处截断或分条，样式标签会让实际消息体比请求体更大。

## B. CardKit 流式

| 步骤 | 接口 | user | bot |
| --- | --- | --- | --- |
| 建卡 | `POST /open-apis/cardkit/v1/cards` | `99991668 user access token not support` | 可以 |
| 发送 | `POST /open-apis/im/v1/messages`，`{"type":"card","data":{"card_id":…}}` | 可以，任何本人所在会话 | 仅限 bot 所在会话，否则 `230002 Bot/User can NOT be out of the chat` |
| 流式写入 | `PUT …/cards/{card_id}/elements/{element_id}/content` | `99991668`，即使卡片由 user 发出 | 可以，对 user 发出的卡同样生效 |
| 结束流式 | `PATCH …/cards/{card_id}/settings` | `99991668` | 可以 |

1. bot 建卡，`card_json` 里开 `config.streaming_mode: true`，正文是一个带 `element_id` 的
   markdown 元素（初始可为空串），`config.summary.content` 决定会话列表的预览文字。
2. user 发送 content 为 `{"type":"card","data":{"card_id":"…"}}` 的 interactive 消息。bot 不必在会话里。
3. bot 反复 `PUT` 元素内容，每次写累积全文，`sequence` 严格递增。
4. bot `PATCH settings` 关闭 `streaming_mode`，`sequence` 接着递增。

实测约束：

- 串行 10 次全部成功，每次约 610–650 ms。
- 回退或重复的 `sequence` 被拒：`300317 sequence number compare failed`。`sequence` 由单一 owner 分配，
  重试用新号。
- 10 个请求带不同 `sequence` 并发写同一张卡：没有频控错误，落后于已接受号的请求返回 `300317`，
  包括最大号在内的其余请求成功，终态是最大号的内容。10 张卡各写一次并发也全部成功。
  所以 B 对乱序是自愈的，但被拒的请求等于白发，仍按卡串行更省。
- user 用 `PATCH /open-apis/im/v1/messages/{id}` 整卡替换 bot 建的卡可以成功，替换后卡片退出流式，
  bot 再写返回 `300309 streaming mode is closed`。bot 写入失败时，user 可以用这条路写终态兜底。
- 流式超时：T+0 写一次后空闲 11 分钟再写，返回 `300309 streaming mode is closed`，服务端已自动关闭流式；
  之后 `PATCH settings` 关闭仍返回成功。超时发生在 0–11 分钟之间，具体阈值、按空闲还是按开启时长计都没测。
  `ai.Stream` 自带 3 分钟超时，一次回答在这之前结束，只要写完立即关流式就碰不到。

## 两条路线共有的事实

- 2.0 卡片的发送响应里 `body.content` 固定是「请升级至最新版本客户端，以查看内容」占位，不代表失败。
- 更新会改写消息体、推进 `update_time` 并置 `updated=true`，本机 sync 能读到中间态与终态，
  自己发出的这类卡片按普通卡片消息刷新，无需特殊处理。

- 本人可以 Recall 这两类卡片（`DELETE /open-apis/im/v1/messages/{id}`，user 身份），bot 建的卡也一样，
  撤回后 `sender_type` 仍是 `user`。

未验证：

- 收件人一侧的发送者展示（是否带应用来源标记）。只能由另一个账号在客户端里看。
- CardKit 的频控上限：10 路并发没有触到，更高并发没有测，AI 回答按卡串行写用不到。

## 与 AI 面板结合

现状：`startAI` 调 `ai.Stream`，chunk 进 `m.aiText` 在右侧面板渲染；`:ai draft …` 结束后把全文放进
composer，由人按 Enter 发送。

设想的新动作「把回答流进会话」：

- 触发与确认。卡片一旦发出，别人就看到正在生成的内容，没有 composer 那一步把关。Lark 客户端没有
  「本人发送流式卡片」的对应交互（最接近的是 AI 助手 bot 的回复），按 Interaction 条款属于自行设计，
  需要显式触发，默认不自动发；发往清单外会话前至少要一次确认。
- 管道。`ai.Chunk` → 合并缓冲 → 单 goroutine 串行写入。同一条消息同时只有一个写请求在途，期间到达的
  chunk 攒进缓冲，下一次写直接带最新全文（latest-wins）。模型出 token 远快于 ~600 ms 一次的写入，
  合并是两条路线的必要条件。A、B 只在「写一次」这一步不同，管道可以对一个写入接口编程，两种实现互换。
- 收尾。Done 时写最终全文（B 再关流式）；Err 或取消时写已得文本加中断标记，不留半截内容或停在流式态的卡。
- lane。一次回答会连续写几十次，放在 interactive lane 会挡按键；需要单独 lane 或借用 beat，宽度为 1
  即可，因为写入本来就按消息串行。
- 方言。卡片 markdown 与 post 不同，`larkmd` 的 lint 目前只覆盖 post；AI 输出进卡片前需要一套卡片方言的
  检查或转换（见 `docs/markdown-lint/DIALECT.md`）。
- 本地回显。右侧 AI 面板本来就在渲染同一份文本，本人看逐段效果不依赖 sync 追上。

## larkcli 缺口

lark-cli 没有这几个接口的 shortcut，只能走 `lark-cli api`，新方法都要在 `larkcli.Fake` 里实现。

- A：发送 inline 卡片 JSON（复用现有 send 路径，msg_type 换成 interactive）；更新消息内容（新增）。
- B：建卡（返回 `card_id`）、写元素全文（带 `sequence`）、关闭流式，发送复用 A 的 send，content 换成
  `card_id` 引用。

## 复现

A：

```sh
# user 发送
lark-cli api POST /open-apis/im/v1/messages --as user --params '{"receive_id_type":"chat_id"}' \
  --data '{"receive_id":"oc_quiet","msg_type":"interactive","content":"{\"schema\":\"2.0\",\"body\":{\"elements\":[{\"tag\":\"markdown\",\"content\":\"\"}]}}"}'

# user 整卡替换，串行
lark-cli api PATCH /open-apis/im/v1/messages/om_elsewhere --as user \
  --data '{"content":"{\"schema\":\"2.0\",\"body\":{\"elements\":[{\"tag\":\"markdown\",\"content\":\"累积到目前的全文\"}]}}"}'
```

B：

```sh
# bot 建卡
lark-cli api POST /open-apis/cardkit/v1/cards --as bot \
  --data '{"type":"card_json","data":"{\"schema\":\"2.0\",\"config\":{\"streaming_mode\":true},\"body\":{\"elements\":[{\"tag\":\"markdown\",\"element_id\":\"md1\",\"content\":\"\"}]}}"}'

# user 发送
lark-cli api POST /open-apis/im/v1/messages --as user --params '{"receive_id_type":"chat_id"}' \
  --data '{"receive_id":"oc_quiet","msg_type":"interactive","content":"{\"type\":\"card\",\"data\":{\"card_id\":\"7000000000000000001\"}}"}'

# bot 写入，sequence 递增
lark-cli api PUT /open-apis/cardkit/v1/cards/7000000000000000001/elements/md1/content --as bot \
  --data '{"content":"累积到目前的全文","sequence":1}'

# bot 结束流式
lark-cli api PATCH /open-apis/cardkit/v1/cards/7000000000000000001/settings --as bot \
  --data '{"settings":"{\"config\":{\"streaming_mode\":false}}","sequence":2}'
```
