# 卡片图片不渲染

TUI 里 `interactive`（卡片）消息的图片一律显示占位 `[图片]`，普通图片消息正常。
`agentctx` 导出同一条消息时标记 `(not downloaded)`。

## 根因

`lark-cli` 的资源提取器不认 `interactive` 消息：
`shortcuts/im/convert_lib/resource_extract.go` 的 `extractResourceRefs` 只覆盖
`image / file / audio / video / media / post / merge_forward`，卡片的
`json_attachment.images[*].origin_key` 从不进入 `--download-resources` 的下载清单。

larkim 侧链路是通的：`sync.ExtractResources` 走 `walkCardAttachment`（`sync/resources.go`）
把卡片图 key 登记成 `resources` 行，但 `downloadPending` 在 lark-cli 返回里找不到该 key，
判 `not returned by lark-cli`；行停在 `status='failed'`、`local_path` 为空，
`tui.pictureRows`（`tui/rows.go`）于是退回一行 `[图片]` 占位。

飞书接口本身对卡片图完全放行，不是权限或接口能力问题：

```
lark-cli api GET '/open-apis/im/v1/messages/om_x100b6473dc29d8b0c10f2c17de29001/resources/img_v3_0215r_d45062ad-a6a0-4f1a-89c9-7cf10a3be78g' \
  --params '{"type":"image"}' -o /tmp/card.png
# => image/png, 417858 bytes, 720x405
```

而同一条消息走 mget 拿不到任何资源：

```
lark-cli im +messages-mget --message-ids om_x100b6473dc29d8b0c10f2c17de29001 --download-resources
# => 返回体无 resources 字段
```

## 影响面（2026-09-24 本机库快照）

```sql
SELECT m.msg_type, r.status, count(*) FROM resources r JOIN messages m USING(message_id) GROUP BY 1,2;
-- interactive|failed|899      错误全是 not returned by lark-cli
-- post|done|526  image|done|318  file|done|21  media|done|13  audio|done|1
```

## 修复要两边动

1. **lark-cli**：`extractResourceRefs` 补 `interactive` 分支，读
   `json_attachment.images[*].origin_key`（`json_attachment` 可能是内联对象，也可能是
   嵌套的 JSON 字符串）。larkim 的 `walkCardAttachment` 已有等价实现，可直接搬。
2. **larkim**：光修 lark-cli 补不回历史数据。899 行里 873 行 `attempts=5`、
   `next_attempt_at=0`，`store.ResourceMessagesDue` 永不再选中它们。需要一条迁移把这些
   `failed` 行重置回 `pending`，形制参考 `0008_rescan_card_images.sql`。

## 同类缺口：merge_forward 图片

`sync.ExtractResources` 对 `merge_forward` 返回 nil（注释假定资源挂在内层消息上），
本机 35 条 merge_forward 里 24 条正文含 `![Image](img_v3_…)`，对应 `resources` 行数为 0，
这些图同样只显示 `[图片]`。

lark-cli 支持下载转发图（`extractMergeForwardResourceRefs`，按顶层容器 id 寻址，
子消息 id 会被接口拒为 `234003 File not in msg`），缺的是 larkim 不登记行、于是从未发起请求。
根因在 larkim 侧，与卡片图是两处独立改动。

## 无影响的已知偏差

`post` 附件区的顶层 `files[]`：lark-cli 提取，larkim 的 `walkPost` 只认 `tag: img/media`
因而不提取。本机库 0 条命中。
