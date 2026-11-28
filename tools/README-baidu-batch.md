# 百度收录查询：无代理控节奏方案

配套脚本：`baidu-batch.py`（仅 Python3 标准库）

## 推荐参数（基于本实例实测）

| 参数 | 建议 | 说明 |
| --- | --- | --- |
| 每批条数 | 8（5–10） | 同步接口上限 20；批越大越接近连续压测 |
| 批间隔 | 300 秒 | 最低不要低于 120 秒 |
| 单条节奏 | 12 秒/条 | config.yaml `baidu.rate_requests: 5`、`rate_burst: 1` |
| 日上限 | **默认不限量** | 需要时用 `--daily-max N` 自行限制；不限额时靠批间隔和验证码暂停控制节奏 |
| 验证码后暂停 | 10 分钟（连续第 2 次 30 分钟） | 与内置冷却 2/10/30 档位配合 |
| 等冷却上限 | 45 分钟 | 超过则停止，进度已存，可重跑续跑 |

依据：仓库实测 5 秒/条 ≈ 1/23 触发验证码，12 秒/条 ≈ 1/130；
本实例 43 条一次性任务触发 3 次验证码，按 2+10+30 分钟阶梯损失约 42 分钟。
拆成 8 条/批、间隔 5 分钟，可把"被动 30 分钟冷却"变成"主动 5 分钟间隔"。

## 用法

```bash
# 先看计划（不发请求）
python3 /opt/web2api/tools/baidu-batch.py --urls urls.txt --batch 8 --gap 300 --dry-run

# 正式跑（默认参数即推荐值；自动等冷却、断点续跑，默认不限量）
python3 /opt/web2api/tools/baidu-batch.py --urls urls.txt

# 自定义
python3 /opt/web2api/tools/baidu-batch.py --urls urls.txt --batch 5 --gap 600

# 如需限制当日条数（默认不限）
python3 /opt/web2api/tools/baidu-batch.py --urls urls.txt --daily-max 100
```

- API Key 默认读 `/root/.web2api-e2e-key`，也可 `--api-key` 或 `W2A_KEY`。
- 结果：`/var/tmp/w2a-baidu-results.jsonl`；进度：`/var/tmp/w2a-baidu-state.json`（按天重置计数，已完成的 URL 不会重复查）。
- 脚本启动前会读 `/web-tool/engines/status`，百度在 cooldown 时自动等待。
- 每批是同步请求（≤20 条），不占用异步任务队列。

## 手动方案（不用脚本）

控制台「在线调试」一次填 ≤8 条，点检测，等 5 分钟再下一批；
或 curl：

```bash
curl -X POST https://radar.ydsdoa.cn/web-tool/v1/index/check \
  -H "X-API-Key: $W2A_KEY" -H "Content-Type: application/json" \
  -d '{"urls":["https://a.com","https://b.com"],"engines":["baidu"]}'
```

## 如果仍频繁验证码

1. 降到每批 5 条、间隔 600 秒；
2. 把 `baidu.rate_requests` 从 5 降到 4（15 秒/条）；
3. 仍不行再考虑代理池（当前不采购）。
