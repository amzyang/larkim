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

## 修复方案（不改 lark-cli）

lark-cli 另有一个按 key 直连下载的 shortcut，绕开缺失的提取器。卡片图与转发图均已验证可取：

```
cd ~/.larkim/resources
lark-cli im +messages-resources-download --message-id om_x100b6473dc29d8b0c10f2c17de29001 \
  --file-key img_v3_0215r_6151b5b4-d347-4157-aaba-f5000681637g --type image \
  --output lark-im-resources/img_v3_0215r_6151b5b4-d347-4157-aaba-f5000681637g
# => {"saved_path":"…/lark-im-resources/img_v3_….png","size_bytes":41936}
```

`--output` 的路径策略允许 cwd 根，而 `ExecClient.Dir` 已是 `cfg.ResourcesDir()`（`cli/root.go`），
落盘位置与 `--download-resources` 一致，扩展名由 lark-cli 按 Content-Type 追加。

### 1. 客户端原语

`larkcli.Client` 增加一个方法，`ExecClient` 与 `Fake` 各自实现：

```go
DownloadResource(ctx context.Context, messageID, fileKey, typ string) (Resource, error)
```

`ExecClient` 执行上面那条命令，解出 `{saved_path, size_bytes}` 填进 `larkcli.Resource`。

### 2. downloadPending 把「lark-cli 没返回」当回退点

```go
res, ok := got[id][p.FileKey]
if !ok {
    if direct >= directPerTick { continue }   // 留到下一 tick，不消耗 attempts
    direct++
    res, err = s.Client.DownloadResource(ctx, id, p.FileKey, p.Type)
    if err != nil { /* failResource，记真实错误 */ }
}
// 往下仍走 storeResource：stat、MaxBytes、Rel 全部复用
```

取回退而非按 `msg_type` 分流：miss 检测本身是精确的，larkim 不必维护一份「lark-cli 支持哪些类型」
的镜像判断；上游补齐后这条分支自然不再触发，也不会重复下载。

预算是必需的。`DownloadPerTick = 1`（50 条消息/tick），而 lark-cli 调用全局串行；一个 tick 内 fork
几十次会把 3s 的 tick 拖成分钟级并饿死 read-status、render 等步骤。取 `directPerTick = 10`，
899 条积压约 4.5 分钟清完。预算用尽必须 `continue`，走 `failResource` 会白耗 attempts。

### 3. 回填历史行

873 行已 `attempts=5`、`next_attempt_at=0`，`store.ResourceMessagesDue` 永不再选中它们。
迁移形制参考 `0008_rescan_card_images.sql`：

```sql
-- 0012_retry_card_images.sql
UPDATE resources SET status = 'pending', attempts = 0, next_attempt_at = 0, last_error = ''
WHERE status = 'failed' AND last_error = 'not returned by lark-cli';
```

按 `last_error` 限定，避免复活真正失败的行。

### 4. 测试

`Fake` 记录 `DownloadResource` 的调用并支持注入错误：

- mget 不返回该 key 时直连被调用，资源行变 `done`
- 超出预算的 key 保持 `pending`，`attempts` 不变
- 直连失败时 `last_error` 记录真实原因

## 同类缺口：merge_forward 图片

`sync.ExtractResources` 对 `merge_forward` 返回 nil（注释假定资源挂在内层消息上），
本机 35 条 merge_forward 里 24 条正文含 `![Image](img_v3_…)`，对应 `resources` 行数为 0，
这些图同样只显示 `[图片]`。

`merge_forward` 的 `content_raw` 是字面量 `Merged and Forwarded Message`，图片 key 只存在于
渲染后的正文里。因此提取只能放在 `storeRendered` 之后，用 `tui/rows.go` 那条 `imgRef` 同形的正则
扫 `content` 注册资源行，下一 tick 由上面的直连路径取——按顶层容器 id 寻址已验证可行
（子消息 id 会被接口拒为 `234003 File not in msg`）。

这是第二个提取来源，与卡片图是两处独立改动，分开提交。

## 无影响的已知偏差

`post` 附件区的顶层 `files[]`：lark-cli 提取，larkim 的 `walkPost` 只认 `tag: img/media`
因而不提取。本机库 0 条命中。
