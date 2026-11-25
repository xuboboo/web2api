# 本机 100 项真实 HTTP 验证

运行时间：2026-11-25T18:51:26+08:00。结果：**100/100 通过**。目标：http://127.0.0.1:17002/web-tool。

每项均为对真实运行二进制的独立 HTTP 请求；结果来自实际响应，未复用源码推断。

| # | 分组 | 验证项 | 请求 | 期望 | 实际 | 判定 |
| --- | --- | --- | --- | --- | --- | --- |
| 001 | 基础可达性 | 健康检查 | GET /health | 200 | 200 | 通过 |
| 002 | 基础可达性 | 官网首页 | GET / | 200 | 200 | 通过 |
| 003 | 基础可达性 | 控制台首页 | GET /console/ | 200 | 200 | 通过 |
| 004 | 基础可达性 | 控制台脚本含新页面 | GET /console/assets/app.js | 200 | 200 | 通过 |
| 005 | 基础可达性 | 引擎状态看板 | GET /engines/status | 200 | 200 | 通过 |
| 006 | 基础可达性 | MCP OpenAPI | GET /openapi.yaml | 200 | 200 | 通过 |
| 007 | 基础可达性 | 未知路径返回 404 | GET /no-such-route | 404 | 404 | 通过 |
| 008 | 基础可达性 | 无密钥公网端点 401 | GET /v1/quota | 401 | 401 | 通过 |
| 009 | 基础可达性 | 错误方法 405 | POST /v1/quota | 405 | 405 | 通过 |
| 010 | 登录与身份 | 管理员登录返回角色 | POST /api/auth/login | 200 | 200 | 通过 |
| 011 | 登录与身份 | /api/me 角色与用户ID | GET /api/me | 200 | 200 | 通过 |
| 012 | 登录与身份 | 错误密码 401 | POST /api/auth/login | 401 | 401 | 通过 |
| 013 | 登录与身份 | 未知用户 401 | POST /api/auth/login | 401 | 401 | 通过 |
| 014 | 登录与身份 | 空字段登录体 401 | POST /api/auth/login | 401 | 401 | 通过 |
| 015 | 登录与身份 | 会话刷新 | POST /api/auth/refresh | 200 | 200 | 通过 |
| 016 | 登录与身份 | 登录设置 HttpOnly Cookie | POST /api/auth/login | 200 | 200 | 通过 |
| 017 | 登录与身份 | Bearer 无 Cookie 可用 | GET /api/me | 200 | 200 | 通过 |
| 018 | 登录与身份 | 伪造 Bearer 401 | GET /api/me | 401 | 401 | 通过 |
| 019 | 用户与角色 | 创建用户 audit_user_00(operator) | POST /api/users | 200 | 200 | 通过 |
| 020 | 用户与角色 | 创建用户 audit_user_01(viewer) | POST /api/users | 200 | 200 | 通过 |
| 021 | 用户与角色 | 创建用户 audit_user_02(viewer) | POST /api/users | 200 | 200 | 通过 |
| 022 | 用户与角色 | 用户列表包含新建账号 | GET /api/users | 200 | 200 | 通过 |
| 023 | 用户与角色 | 重复用户名 400 | POST /api/users | 400 | 400 | 通过 |
| 024 | 用户与角色 | 非法角色 400 | POST /api/users | 400 | 400 | 通过 |
| 025 | 用户与角色 | 过短用户名 400 | POST /api/users | 400 | 400 | 通过 |
| 026 | 用户与角色 | 弱密码 400 | POST /api/users | 400 | 400 | 通过 |
| 027 | 用户与角色 | 运维员登录 | POST /api/auth/login | 200 | 200 | 通过 |
| 028 | 用户与角色 | 只读用户登录 | POST /api/auth/login | 200 | 200 | 通过 |
| 029 | 用户与角色 | 只读用户访问用户管理 403 | GET /api/users | 403 | 403 | 通过 |
| 030 | 用户与角色 | 只读用户 /api/me 200 | GET /api/me | 200 | 200 | 通过 |
| 031 | 用户与角色 | 运维员访问用户管理 403 | GET /api/users | 403 | 403 | 通过 |
| 032 | 用户与角色 | 运维员可列黑名单 200 | GET /api/blacklist | 200 | 200 | 通过 |
| 033 | 用户与角色 | 运维员可创建密钥 200 | POST /api/keys | 200 | 200 | 通过 |
| 034 | 用户与角色 | 只读用户创建密钥 403 | POST /api/keys | 403 | 403 | 通过 |
| 035 | 用户与角色 | 只读用户日志只读 200 | GET /api/logs | 200 | 200 | 通过 |
| 036 | 用户与角色 | 只读用户清理日志 403 | POST /api/logs/cleanup | 403 | 403 | 通过 |
| 037 | 用户与角色 | 提升只读用户为运维员 200 | PATCH /api/users/991d937da2aeaaae4ddf898d | 200 | 200 | 通过 |
| 038 | 用户与角色 | 角色变更后旧令牌失效 401 | GET /api/me | 401 | 401 | 通过 |
| 039 | 用户与角色 | 新角色登录生效 | POST /api/auth/login | 200 | 200 | 通过 |
| 040 | 会话与密码 | 会话列表含当前会话 | GET /api/sessions | 200 | 200 | 通过 |
| 041 | 会话与密码 | 运维员禁用自身被角色守卫拒绝 403 | PATCH /api/users/01940c0422c8cc2322b2d08c | 403 | 403 | 通过 |
| 042 | 会话与密码 | 运维员删除自身被角色守卫拒绝 403 | DELETE /api/users/01940c0422c8cc2322b2d08c | 403 | 403 | 通过 |
| 043 | 会话与密码 | 非法状态 400 | PATCH /api/users/ac1f7fa23c204d9630f38d19 | 400 | 400 | 通过 |
| 044 | 会话与密码 | 重置用户密码 200 | POST /api/users/ac1f7fa23c204d9630f38d19/password | 200 | 200 | 通过 |
| 045 | 会话与密码 | 旧密码失效 401 | POST /api/auth/login | 401 | 401 | 通过 |
| 046 | 会话与密码 | 新密码可登录 200 | POST /api/auth/login | 200 | 200 | 通过 |
| 047 | 会话与密码 | 禁用用户 200 | PATCH /api/users/ac1f7fa23c204d9630f38d19 | 200 | 200 | 通过 |
| 048 | 会话与密码 | 被禁用用户无法登录 401 | POST /api/auth/login | 401 | 401 | 通过 |
| 049 | 会话与密码 | 不存在用户删除 404 | DELETE /api/users/00000000000000000000000000000000 | 404 | 404 | 通过 |
| 050 | 密钥与公网 | 创建密钥 200 | POST /api/keys | 200 | 200 | 通过 |
| 051 | 密钥与公网 | 密钥列表包含新密钥 | GET /api/keys | 200 | 200 | 通过 |
| 052 | 密钥与公网 | 密钥查询配额 200 | GET /v1/quota | 200 | 200 | 通过 |
| 053 | 密钥与公网 | 密钥调用收录检查 200 | GET /v1/index/check?url=http://127.0.0.1/&engines=baidu | 200 | 200 | 通过 |
| 054 | 密钥与公网 | 伪造密钥 401 | GET /v1/quota | 401 | 401 | 通过 |
| 055 | 密钥与公网 | 未知引擎参数 400 | GET /v1/index/check?url=http://127.0.0.1/&engines=nope | 400 | 400 | 通过 |
| 056 | 密钥与公网 | 轮换密钥 200 | POST /api/keys/5caa57b9676d1d5c/rotate | 200 | 200 | 通过 |
| 057 | 密钥与公网 | 禁用密钥 200 | PATCH /api/keys/5caa57b9676d1d5c | 200 | 200 | 通过 |
| 058 | 密钥与公网 | 禁用后密钥不可用 401 | GET /v1/quota | 401 | 401 | 通过 |
| 059 | 密钥与公网 | 用量明细 200 | GET /api/usage?limit=5 | 200 | 200 | 通过 |
| 060 | 黑名单 | 添加 IP 黑名单 200 | POST /api/blacklist | 200 | 200 | 通过 |
| 061 | 黑名单 | 黑名单列表含新规则 | GET /api/blacklist | 200 | 200 | 通过 |
| 062 | 黑名单 | 重复黑名单 400 | POST /api/blacklist | 400 | 400 | 通过 |
| 063 | 黑名单 | 非法 IP 400 | POST /api/blacklist | 400 | 400 | 通过 |
| 064 | 黑名单 | 非法 CIDR 400 | POST /api/blacklist | 400 | 400 | 通过 |
| 065 | 黑名单 | 自动推断 CIDR 类型 200 | POST /api/blacklist | 200 | 200 | 通过 |
| 066 | 黑名单 | 修改黑名单原因 200 | PATCH /api/blacklist/2393f4fcd9fd6686 | 200 | 200 | 通过 |
| 067 | 黑名单 | 设置过期时间 200 | PATCH /api/blacklist/2393f4fcd9fd6686 | 200 | 200 | 通过 |
| 068 | 黑名单 | 过去过期时间 400 | PATCH /api/blacklist/2393f4fcd9fd6686 | 400 | 400 | 通过 |
| 069 | 黑名单 | 运维员角色加黑名单 | POST /api/blacklist | 200 | 200 | 通过 |
| 070 | 黑名单 | 不存在规则删除 404 | DELETE /api/blacklist/does-not-exist | 404 | 404 | 通过 |
| 071 | 黑名单 | CIDR 列表存在 | GET /api/blacklist | 200 | 200 | 通过 |
| 072 | 黑名单 | 删除 CIDR 规则 200 | DELETE /api/blacklist/ed2ae646dd542c50 | 200 | 200 | 通过 |
| 073 | 黑名单 | 删除 IP 规则 200 | DELETE /api/blacklist/2393f4fcd9fd6686 | 200 | 200 | 通过 |
| 074 | 日志 | 日志配置生效 | GET /api/logs/config | 200 | 200 | 通过 |
| 075 | 日志 | 日志查询非空 | GET /api/logs | 200 | 200 | 通过 |
| 076 | 日志 | 按级别过滤 info | GET /api/logs?level=info | 200 | 200 | 通过 |
| 077 | 日志 | 按关键字过滤 | GET /api/logs?q=%E7%99%BB%E5%BD%95 | 200 | 200 | 通过 |
| 078 | 日志 | 未来 since 返回空 | GET /api/logs?since=2099-01-01T00:00:00%2B08:00 | 200 | 200 | 通过 |
| 079 | 日志 | 分页 limit=1 | GET /api/logs?limit=1 | 200 | 200 | 通过 |
| 080 | 日志 | 非法 since 400 | GET /api/logs?since=not-a-time | 400 | 400 | 通过 |
| 081 | 日志 | 手动清理返回计数 | POST /api/logs/cleanup | 200 | 200 | 通过 |
| 082 | 日志 | 审计轨迹含用户操作 | GET /api/audit | 200 | 200 | 通过 |
| 083 | 稳定性与边界 | 连续 /api/me 第1批 | GET /api/me | 200 | 200 | 通过 |
| 084 | 稳定性与边界 | 连续 /api/me 第2批 | GET /api/me | 200 | 200 | 通过 |
| 085 | 稳定性与边界 | 连续 /api/me 第3批 | GET /api/me | 200 | 200 | 通过 |
| 086 | 稳定性与边界 | 无凭据 401 信封 | GET /api/me | 401 | 401 | 通过 |
| 087 | 稳定性与边界 | 角色不足 403 信封 | GET /api/users | 403 | 403 | 通过 |
| 088 | 稳定性与边界 | 404 信封 | GET /api/nope | 404 | 404 | 通过 |
| 089 | 稳定性与边界 | 日志不含令牌原文 | GET /api/logs?limit=200 | 200 | 200 | 通过 |
| 090 | 稳定性与边界 | 审计不含密码原文 | GET /api/audit?limit=200 | 200 | 200 | 通过 |
| 091 | 稳定性与边界 | 会话撤销 200 | POST /api/auth/logout | 200 | 200 | 通过 |
| 092 | 稳定性与边界 | 撤销后令牌 401 | GET /api/me | 401 | 401 | 通过 |
| 093 | 稳定性与边界 | 备份快照可下载 | GET /api/admin/backup | 200 | 200 | 通过 |
| 094 | 稳定性与边界 | 对账端点 200 | GET /api/reconcile | 200 | 200 | 通过 |
| 095 | 稳定性与边界 | 用量导出 200 | GET /api/usage/export?format=csv | 200 | 200 | 通过 |
| 096 | 稳定性与边界 | 冷却操作 200 | POST /api/engines/baidu/cooldown/reset | 200 | 200 | 通过 |
| 097 | 稳定性与边界 | 引擎快照只读 200 | GET /api/engines | 200 | 200 | 通过 |
| 098 | 稳定性与边界 | 系统信息只读 200 | GET /api/runtime | 200 | 200 | 通过 |
| 099 | 黑名单终检 | 最终：添加本机 IP 黑名单 200 | POST /api/blacklist | 200 | 200 | 通过 |
| 100 | 黑名单终检 | 最终：黑名单拦截请求 403 | GET /api/me | 403 | 403 | 通过 |

原始证据：qa/features-audit/http-results.json。
