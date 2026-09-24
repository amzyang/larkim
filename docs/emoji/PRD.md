# 表情体系

- lark official reactions
- custom emoji pictures

## 在消息列表中展示消息相关的 reactions

- 排序与 lark 端一致：按每个 emoji 最早一次 action_time 升序；details 覆盖不到的 emoji 排在最后
- 展示参与者而非计数：最多 3 个名字，其余记为 +n
- 自己显示为「你」，参与者不加颜色或下划线
- 联系人里查不到的 operator 不写 open id，计入 +n

## 在会话列表中展示 reactions

- 仅 p2p，群聊的 reaction 留在消息面板
- 只显示 emoji icon（unicode 字符或图片），不显示计数与参与者
- 放在 summary 行最前面：`{reaction icons} {summary}`
- 最多 3 个；既没有字符也没有图片的 emoji 直接省略
- 撤回的消息不显示

## 在消息列表中添加、移除对消息的 reaction

## 在 terminal 中展示 emoji

- unicode/ascii emoji
- lark reaction: 使用对等的 unicode/ascii emoji，或者是使用图片替代
- custom emoji pictures: 使用图片替代

构建一个静态库，维护好映射关系和图片资源。然后 larkim 消费

使用的 lark application: /Applications/sagtjy516.app
已有资源: 本机的 lark-emoji-alfred workflow（表情别名与拼音词表）。

support using fzf to quickly search and select emoji. need to support pinyin, fuzzy etc
