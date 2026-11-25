# 十维度 100 项真机测试

运行时间：2026-11-25T20:20:38+08:00 — 2026-11-25T20:20:47+08:00。结果：**100/100 通过**。被测二进制：C:\Users\YDSD\AppData\Local\Temp\w2a-audit100.exe。

每项均为对真实运行实例（HTTP/CLI/文件系统）的独立验证，证据逐项保存在 qa/features-audit/multidim-results.json。

| # | 维度 | 验证项 | 判定 | 证据摘要 |
| --- | --- | --- | --- | --- |
| 001 | 契约 | 健康检查 | 通过 | status=200 body={"status":"healthy","uptime":"0s","engines":[{"name":"baidu","initialized":true,"status":"ready"},{"name |
| 002 | 契约 | 官网首页 | 通过 | <!DOCTYPE html>
<html lang="zh-CN" class="no-js">
<head>
<meta charset="utf-8">
 |
| 003 | 契约 | 控制台首页 | 通过 | <!DOCTYPE html>
<html lang="zh-CN">
<head>
  <meta charset="UTF-8">
  <meta name |
| 004 | 契约 | 控制台脚本含三个新页面 | 通过 |  |
| 005 | 契约 | 引擎状态看板 | 通过 | status=200 body={"engines":[{"breaker":{"engine":"baidu","failure_count":0,"state":"closed"},"consecutive_blocks":0,"ena |
| 006 | 契约 | MCP OpenAPI 文档 | 通过 | status=200 body=openapi: 3.1.0
jsonSchemaDialect: https://json-schema.org/draft/2020-12/schema
info:
  title: Web2API |
| 007 | 契约 | MCP llms 文档 | 通过 | status=200 body=# Web2API 网站收录查询开放平台

> 单二进制自托管的网站收录查询开放平台:百度/搜狗/360/必应四引擎的网页搜索与 URL 收录判定,内建 API 密钥鉴权、配额计费、限流与引擎冷却风控;为 |
| 008 | 契约 | 未知路径 404 | 通过 | status=404 body=<!DOCTYPE html>
<html lang="zh-CN">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=d |
| 009 | 契约 | 无密钥公网端点 401 | 通过 | status=401 body={"error":"unauthorized"} |
| 010 | 契约 | 错误方法 405 | 通过 | status=405 body={"error":"method_not_allowed","code":405,"request_id":"01a212a6-9d5a-7c9c-a43d-901744e5789f","message":" |
| 011 | 契约 | 无凭据 /api/me 401 信封 | 通过 | {"error":"unauthorized"} |
| 012 | 契约 | 已鉴权 /api/runtime 200 | 通过 | status=200 body={"go_version":"go1.26.11","goroutines":12,"memory_mb":1,"uptime_seconds":0,"version":"0.8.19"} |
| 013 | 角色 | 管理员登录含角色 | 通过 | {"expires_in":86400,"token":"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJhZG1pbiIsImlhdCI6MTc5NTYwOTIzOSwiZXhwIjoxNz |
| 014 | 角色 | me 返回角色与用户ID | 通过 | {"keys_count":0,"role":"admin","today_calls":0,"total_calls":0,"user_id":"5c0440d318ea687a76602f12","username":"admin"," |
| 015 | 角色 | 创建运维员 | 通过 | {"ok":true,"user":{"id":"12ebfffdfdcda672b5ee8037","username":"md_operator","role":"operator","status":"active","created |
| 016 | 角色 | 创建只读用户 | 通过 | {"ok":true,"user":{"id":"73c1117d4557f43945110f0f","username":"md_viewer","role":"viewer","status":"active","created_at" |
| 017 | 角色 | 重复用户名 400 | 通过 | {"code":"INVALID_PARAM","error":"bad_request","message":"用户名已存在","reason":"INVALID_PARAM"} |
| 018 | 角色 | 非法角色 400 | 通过 | {"code":"INVALID_PARAM","error":"bad_request","message":"角色无效(admin/operator/viewer)","reason":"INVALID_PARAM"} |
| 019 | 角色 | 弱密码 400 | 通过 | {"code":"INVALID_PARAM","error":"bad_request","message":"密码长度至少需要 8 个字符","reason":"INVALID_PARAM"} |
| 020 | 角色 | 运维员登录成功 | 通过 | token present |
| 021 | 角色 | 只读用户登录成功 | 通过 | token present |
| 022 | 角色 | 只读用户用户管理 403 | 通过 | {"code":"insufficient_role","error":"forbidden"} |
| 023 | 角色 | 只读用户日志查询 200 | 通过 | status=200 body={"items":[{"time":"2026-11-25T20:20:39+08:00","level":"warning","message":"\"request error\"","source":" |
| 024 | 角色 | 运维员黑名单列表 200 | 通过 | status=200 body={"items":[],"total":0} |
| 025 | 角色 | 只读用户创建密钥 403 | 通过 | status=403 body={"code":"insufficient_role","error":"forbidden"} |
| 026 | 角色 | PATCH 修改角色 200 | 通过 | status=200 body={"ok":true,"user":{"id":"73c1117d4557f43945110f0f","username":"md_viewer","role":"operator","status":"ac |
| 027 | 角色 | 角色变更后旧令牌 401 | 通过 | status=401 body={"error":"unauthorized"} |
| 028 | 会话 | 刷新返回新令牌 | 通过 | {"expires_in":86400,"token":"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJhZG1pbiIsImlhdCI6MTc5NTYwOTIzOSwiZXhwIjoxNz |
| 029 | 会话 | 会话列表标记当前会话 | 通过 | {"items":[{"id":"23e2599c11990ea2e04686c612d1f563719dc4edc91a000f40ea840b6c4a5afc","created_at":"2026-11-25T20:20:38.702 |
| 030 | 会话 | 篡改令牌 401 | 通过 | status=401 body={"error":"unauthorized"} |
| 031 | 会话 | 登录 Set-Cookie HttpOnly | 通过 | {'Date': 'Wed, 25 Nov 2026 12:20:39 GMT', 'Content-Type': 'application/json', 'Content-Length': '516', 'X-Content-Type-O |
| 032 | 会话 | Bearer 无 Cookie 可用 | 通过 | status=200 body={"keys_count":0,"role":"admin","today_calls":0,"total_calls":0,"user_id":"5c0440d318ea687a76602f12","use |
| 033 | 会话 | 运维员自助改密 200 | 通过 | status=200 body={"expires_in":86400,"ok":true,"token":"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJtZF9vcGVyYXRvciIs |
| 034 | 会话 | 改密后旧令牌 401 | 通过 | status=401 body={"error":"unauthorized"} |
| 035 | 会话 | 新密码可登录 | 通过 | {"expires_in":86400,"token":"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJtZF9vcGVyYXRvciIsImlhdCI6MTc5NTYwOTIzOSwiZX |
| 036 | 会话 | 管理员重置用户密码 200 | 通过 | status=200 body={"ok":true} |
| 037 | 会话 | 重置后旧密码 401 | 通过 | status=401 body={"error":"unauthorized"} |
| 038 | 并发 | 20 并发 /api/me 全成功 | 通过 | statuses=[200] |
| 039 | 并发 | 20 并发同名用户创建仅一个成功 | 通过 | ok=1 |
| 040 | 并发 | 20 并发同值黑名单仅一个成功 | 通过 | ok=1 |
| 041 | 并发 | 10 并发创建密钥全成功 | 通过 | statuses=[200] |
| 042 | 并发 | 10 并发日志查询全成功 | 通过 | statuses=[200] |
| 043 | 并发 | 10 并发登录全成功 | 通过 | statuses=[200] |
| 044 | 并发 | 10 并发伪造令牌全 401 | 通过 | statuses=[401] |
| 045 | 并发 | 10 并发用户列表全成功 | 通过 | statuses=[200] |
| 046 | 并发 | 并发降级不清除全部管理员 | 通过 | statuses=[200] |
| 047 | 并发 | 压力后健康检查 200 | 通过 | status=200 body={"status":"healthy","uptime":"2s","engines":[{"name":"baidu","initialized":true,"status":"ready"},{"name |
| 048 | 持久化 | 重启前创建持久用户 | 通过 | {"ok":true,"user":{"id":"f9680cdc46b5e8a6bc28812b","username":"md_persist","role":"viewer","status":"active","created_at |
| 049 | 持久化 | 重启前创建持久密钥 | 通过 | {"id":"ac43af6a605d4c86","name":"persist-key","prefix":"sk-w2a-cc45f3","status":"active","daily_quota":0,"used_today":0, |
| 050 | 持久化 | 重启后健康可用 | 通过 | status=200 body={"status":"healthy","uptime":"0s","engines":[{"name":"baidu","initialized":true,"status":"ready"},{"name |
| 051 | 持久化 | 重启后旧管理员令牌仍有效 | 通过 | status=200 body={"keys_count":11,"role":"admin","today_calls":0,"total_calls":0,"user_id":"5c0440d318ea687a76602f12","us |
| 052 | 持久化 | 重启后用户可登录 | 通过 | {"expires_in":86400,"token":"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJtZF9wZXJzaXN0IiwiaWF0IjoxNzk1NjA5MjQxLCJleH |
| 053 | 持久化 | 重启后用户角色保持 | 通过 | {"expires_in":86400,"token":"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJtZF9wZXJzaXN0IiwiaWF0IjoxNzk1NjA5MjQxLCJleH |
| 054 | 持久化 | 重启后黑名单规则仍在 | 通过 | {"items":[{"id":"b9085b7e3b7d8764","kind":"ip","value":"203.0.113.55","created_by":"admin","created_at":"2026-11-25T20:2 |
| 055 | 持久化 | 重启后密钥仍可用 | 通过 | status=200 body={"key_id":"ac43af6a605d4c86","name":"persist-key","daily_quota":0,"used_today":0,"unlimited":true,"used_ |
| 056 | 持久化 | 重启后审计轨迹仍在 | 通过 | {"items":[{"ts":"2026-11-25T20:20:41.5118559+08:00","actor":"md_persist","ip":"127.0.0.1","action":"login.success","targ |
| 057 | 持久化 | 配置密码变更后新密码可登录 | 通过 | {"expires_in":86400,"token":"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJhZG1pbiIsImlhdCI6MTc5NTYwOTI0MiwiZXhwIjoxNz |
| 058 | 持久化 | 配置密码变更后旧密码失效 | 通过 | status=401 body={"error":"unauthorized"} |
| 059 | 配置 | 严格模式拒绝未知配置键 | 通过 | exit=1 err=Error: WEB2API_STRICT_CONFIG: 未知配置键(已按内置默认值处理): app.definitely_not_a_key — 请核对拼写,完整键表见 docs/CONFIGURATION.md
 |
| 060 | 配置 | 严格模式接受含新键的正式配置 | 通过 | status=200 body={"status":"healthy","uptime":"0s","engines":[{"name":"baidu","initialized":true,"status":"ready"},{"name |
| 061 | 配置 | 环境变量覆盖日志保留天数 | 通过 | retention_days=7 want 7 |
| 062 | 配置 | 环境变量覆盖日志格式为 JSON | 通过 | format='json' want 'json' |
| 063 | 配置 | log_max_mb=0 仍报告配置并可清理 | 通过 | {"audit_retention_days":7,"backups":5,"file_logging_enabled":true,"format":"json","max_mb":0,"retention_days":7} |
| 064 | 配置 | data_plane_auth=false 时统计匿名可访问 | 通过 | status=200 body={"cache":{"bypasses":0,"entries":0,"evictions":0,"hits":0,"max_size":1000,"misses":0,"status":true,"ttl_ |
| 065 | 配置 | 低管理限速触发 429 | 通过 | statuses=[200, 429, 429] |
| 066 | 配置 | 低登录失败阈值触发锁定 | 通过 | statuses=[401, 401, 429] |
| 067 | CLI | admin-reset 退出码为 0 | 通过 | exit=0 err= |
| 068 | CLI | 离线重置后新密码可登录 | 通过 | {"expires_in":86400,"token":"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJhZG1pbiIsImlhdCI6MTc5NTYwOTI0NCwiZXhwIjoxNz |
| 069 | CLI | 离线重置后旧密码失效 | 通过 | status=401 body={"error":"unauthorized"} |
| 070 | CLI | 离线重置使旧令牌失效 | 通过 | status=401 body={"error":"unauthorized"} |
| 071 | CLI | admin-backup 生成快照 | 通过 | exit=0 size=65536 |
| 072 | CLI | 快照包含新增桶数据 | 通过 | size=65536 |
| 073 | CLI | admin-restore 接受快照 | 通过 | exit=0 err= |
| 074 | CLI | 恢复后的数据目录可登录 | 通过 | {"expires_in":86400,"token":"eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiJhZG1pbiIsImlhdCI6MTc5NTYwOTI0NSwiZXhwIjoxNz |
| 075 | 日志 | 日志配置来自环境变量 | 通过 | {"audit_retention_days":1,"backups":2,"file_logging_enabled":true,"format":"text","max_mb":1,"retention_days":1} |
| 076 | 日志 | 日志查询非空 | 通过 | {"items":[{"time":"2026-11-25T20:20:47+08:00","level":"info","message":"\"request completed\"","source":"logs.txt"},{"ti |
| 077 | 日志 | 按 info 级别过滤 | 通过 | {"items":[{"time":"2026-11-25T20:20:47+08:00","level":"info","message":"\"request completed\"","source":"logs.txt"},{"ti |
| 078 | 日志 | 按关键字过滤 | 通过 | {"items":[{"time":"2026-11-25T20:20:47+08:00","level":"info","message":"\"request completed\"","source":"logs.txt"},{"ti |
| 079 | 日志 | 未来 since 返回空 | 通过 | total=0 want 0 |
| 080 | 日志 | 分页 limit/offset | 通过 | {"items":[{"time":"2026-11-25T20:20:47+08:00","level":"info","message":"\"request completed\"","source":"logs.txt"},{"ti |
| 081 | 日志 | 大小轮转生成 logs.txt.1 | 通过 | exists=True size=1512000 |
| 082 | 日志 | 手动清理删除过期归档 | 通过 | {"ok":true,"removed_audit":1,"removed_serve":1} |
| 083 | 日志 | 清理后当前日志仍在 | 通过 | live log present |
| 084 | 黑名单 | 添加 IP 规则 | 通过 | status=200 body={"entry":{"id":"e5aca95801bd1b25","kind":"ip","value":"203.0.113.77","created_by":"admin","created_at":" |
| 085 | 黑名单 | 添加 CIDR 规则 | 通过 | status=200 body={"entry":{"id":"617b33e966e31da6","kind":"cidr","value":"198.51.100.0/24","created_by":"admin","created_ |
| 086 | 黑名单 | 非法 IP 400 | 通过 | status=400 body={"code":"INVALID_PARAM","error":"bad_request","message":"IP 地址格式无效","reason":"INVALID_PARAM"} |
| 087 | 黑名单 | 非法 CIDR 400 | 通过 | status=400 body={"code":"INVALID_PARAM","error":"bad_request","message":"CIDR 格式无效(如 203.0.113.0/24)","reason":"INVALID_ |
| 088 | 黑名单 | 重复规则 400 | 通过 | status=400 body={"code":"INVALID_PARAM","error":"bad_request","message":"该 IP/CIDR 已在黑名单中","reason":"INVALID_PARAM"} |
| 089 | 黑名单 | 自锁保护默认拒绝 | 通过 | status=400 body={"code":"INVALID_PARAM","error":"bad_request","message":"不能屏蔽当前请求来源 IP;如确需封禁自身请显式设置 force:true","reason" |
| 090 | 黑名单 | force 显式允许封禁自身 | 通过 | status=200 body={"entry":{"id":"c80880c90a5b1802","kind":"ip","value":"127.0.0.1","created_by":"admin","created_at":"202 |
| 091 | 黑名单 | 命中后请求 403 blacklisted | 通过 | {"error":"forbidden","reason":"blacklisted"} |
| 092 | 黑名单 | 过去过期时间 400 | 通过 | status=400 body={"code":"INVALID_PARAM","error":"bad_request","message":"过期时间须为 RFC3339 且晚于当前时间","reason":"INVALID_PARAM |
| 093 | 黑名单 | 列表条目含 expired 派生字段 | 通过 | {"items":[{"id":"5f75c795dfdd7f42","kind":"ip","value":"192.0.2.30","created_by":"admin","created_at":"2026-11-25T20:20: |
| 094 | 脱敏 | 日志响应不含管理员令牌 | 通过 | clean |
| 095 | 脱敏 | 审计响应不含 CLI 密码 | 通过 | clean |
| 096 | 脱敏 | 角色不足 403 信封 | 通过 | {"code":"insufficient_role","error":"forbidden"} |
| 097 | 脱敏 | 未授权 401 信封 | 通过 | {"error":"unauthorized"} |
| 098 | 脱敏 | 404 平台信封 | 通过 | {"error":"not_found","code":404,"request_id":"01a212a6-bec6-7aa0-84f9-6e25a7b684a0","message":"Cannot GET /web-tool/api/ |
| 099 | 脱敏 | 登录 Cookie 带 SameSite=Strict | 通过 | {'Date': 'Wed, 25 Nov 2026 12:20:47 GMT', 'Content-Type': 'application/json', 'Content-Length': '516', 'X-Content-Type-O |
| 100 | 脱敏 | 全量测试后服务仍健康 | 通过 | status=200 body={"status":"healthy","uptime":"1s","engines":[{"name":"baidu","initialized":true,"status":"ready"},{"name |
