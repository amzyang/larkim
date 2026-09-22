# 同步飞书数据

常驻后台进程，同步存储到本地 sqlite，直接通过 cli 来消费，或者直接消费 sqlite 文件（但这个需要知道 schema 细节）。需要获取 user 身份、权限的消息和内容
使用  lark cli ~/Vcs/lark/cli/ ，或者说是否可以把它当成一个库来使用
## 聊天消息

包括消息结构、联系人、资源(imgae, file, etc)

## 联系人

###  获取消息列表

- 过滤、排序、分页

技术架构使用 modern golang. cobra. homebrew . dependabot.
### 应用

- 记录只读、未读、消费未读、使用 background computer use, jev 快速标记为已读
