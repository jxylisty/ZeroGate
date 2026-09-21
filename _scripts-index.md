# 散落测试脚本索引（2026-09 归档）

> 这批 `.py` 来自对 ZeroGate 网关及其上游 CodeBuddy 客户端的**探测与反向**：
> - `_probe\`：探测脚本，在 CodeBuddy 前端 `index.js` / webview chunk 中正则检索关键实现（认证头、端点、模型、trace 头、Tencent/Webview API 等），用于定位协议与字段。
> - `_tests\`：对网关 `127.0.0.1:7863` 或 CodeBuddy 接口的连通/功能测试。
> - `_extract\`：从 CodeBuddy 本地 sqlite(vscdb)/DPAPI/AES-GCM 中抽取会话、凭证、密钥、配置文件与路径。
> 原散落在 `src-true\` 根目录，2026-09 按上述分类归档并生成本索引。

## 探针类 `_probe\`（31）

| 新路径（相对 src-true） | 原文件名 | 用途 |
|---|---|---|
| `_probe/probe_apikey.py` | probe_apikey.py | 检索 X-Api-Key / X-Device-Token 等 API Key 头的用法 |
| `_probe/probe_auth.py` | probe_auth.py | 定位 buildAuthHeaders 实现与认证头字段 |
| `_probe/probe_auth2.py` | probe_auth2.py | 检索 plugin/auth、X-Device-Token、deviceToken 等认证相关标记 |
| `_probe/probe_auth3.py` | probe_auth3.py | 检索登录/refresh 会话相关路径（login/account、token/refresh 等） |
| `_probe/probe_auth4.py` | probe_auth4.py | 检索 enterpriseHeaders、getAccounts、fetchTokenByTmpCode |
| `_probe/probe_authdef.py` | probe_authdef.py | 定位 buildAuthHeaders 的定义处并打印上下文 |
| `_probe/probe_authmgr.py` | probe_authmgr.py | 按偏移打印某认证相关模块的片段 |
| `_probe/probe_bundle.py` | probe_bundle.py | 检索 planning-genie / chat/completions 在 bundle 中的使用 |
| `_probe/probe_chatbuild.py` | probe_chatbuild.py | 检索聊天请求构建相关标记（X-Product-Token 等） |
| `_probe/probe_chat_call.py` | probe_chat_call.py | 检索 ensureChatCompletionTraceHeaders、chat/queue/status |
| `_probe/probe_config.py` | probe_config.py | 定位 waitConfiguration 定义 |
| `_probe/probe_config2.py` | probe_config2.py | 检索 "/v2/config" 出现位置及上下文 |
| `_probe/probe_config3.py` | probe_config3.py | 检索 /config/models、/v3/config 等配置端点 |
| `_probe/probe_configurl.py` | probe_configurl.py | 检索 /v2/configurations 等候选配置 URL |
| `_probe/probe_crypto.py` | probe_crypto.py | 检索 v10 加密与密钥相关标记（AES-GCM、sodium 等） |
| `_probe/probe_endpoints.py` | probe_endpoints.py | 用真实会话令牌实测 CodeBuddy 各处端点 |
| `_probe/probe_enterprise_headers.py` | probe_enterprise_headers.py | 定位 enterpriseHeaders( 定义与方法体 |
| `_probe/probe_modelids.py` | probe_modelids.py | 检索模型 ID 与 chat/completions、modelList |
| `_probe/probe_models.py` | probe_models.py | 检索 /v2/models、chatModels 等模型配置 |
| `_probe/probe_module282922.py` | probe_module282922.py | 转储指定偏移的 module 282922 20KB 片段 |
| `_probe/probe_tapi.py` | probe_tapi.py | 定位 TencentAPIImpl 类定义 |
| `_probe/probe_tencentapi.py` | probe_tencentapi.py | 转储某偏移的 Tencent API 相关 15KB 片段 |
| `_probe/probe_trace2.py` | probe_trace2.py | 打印 ensureChatCompletionTraceHeaders 定义身 |
| `_probe/probe_trace3.py` | probe_trace3.py | 定位模块头 538647 上下文 |
| `_probe/probe_trace4.py` | probe_trace4.py | 转储某偏移 16KB 以还原 trace 头模块 |
| `_probe/probe_tracehdr.py` | probe_tracehdr.py | 转储 OpenAIAPIImpl chat 区以看 trace 头来源 |
| `_probe/probe_trace_headers.py` | probe_trace_headers.py | 在 webviews chunk 中检索 ensureChatCompletionTraceHeaders |
| `_probe/probe_vault.py` | probe_vault.py | 检索 secretVault / 加密键 / 会话持久化文件 |
| `_probe/probe_webview.py` | probe_webview.py | 检索 getWebviewInfo 在 index.js 中的用法 |
| `_probe/probe_webview2.py` | probe_webview2.py | 打印 getWebviewInfo 各处命中片段 |
| `_probe/probe_webview_model.py` | probe_webview_model.py | 遍历 webviews 目录 chunk 检索模型 ID/service 字段 |

## 测试类 `_tests\`（11）

| 新路径（相对 src-true） | 原文件名 | 用途 |
|---|---|---|
| `_tests/test_chat_matrix.py` | test_chat_matrix.py | 用变量组合测试 CodeBuddy /v2/chat/completions 头与响应 |
| `_tests/test_chat_system.py` | test_chat_system.py | 测试带 system 消息的 chat 请求与不同 header 变体 |
| `_tests/test_config_get.py` | test_config_get.py | 请求 CodeBuddy 配置接口验证返回 |
| `_tests/test_config_models.py` | test_config_models.py | 请求配置接口并核对 models 列表 |
| `_tests/test_deepseek.py` | test_deepseek.py | 通过网关 7863 测试 DeepSeek 各难度问答 |
| `_tests/test_deepseek_code.py` | test_deepseek_code.py | 测试 DeepSeek 4.1 Flash 代码能力 |
| `_tests/test_deepseek_direct.py` | test_deepseek_direct.py | 直接请求 DeepSeek 4.1 Flash 国际版 API |
| `_tests/test_model_slots.py` | test_model_slots.py | GET buckets 类接口测试模型槽位/round 匹配 |
| `_tests/test_refresh_quota.py` | test_refresh_quota.py | 测试 refreshAmounts/refresh 额度刷新接口 |
| `_tests/test_stream_raw.py` | test_stream_raw.py | 以 claude-3.7-sonnet 流式请求验证 raw 流式输出 |
| `_tests/show_models.py` | show_models.py | 从配置 JSON 展示模型 ID 与相关模型/促销 |

## 提取类 `_extract\`（13）

| 新路径（相对 src-true） | 原文件名 | 用途 |
|---|---|---|
| `_extract/dump_cb_creds.py` | dump_cb_creds.py | 从 state.vscdb 提取 secret 里的 accessToken 等凭证 |
| `_extract/dump_cb_secrets.py` | dump_cb_secrets.py | 列出 CodeBuddy 两个 vscdb 的表与 secret 键 |
| `_extract/dump_cb_state.py` | dump_cb_state.py | 从全局状态取 Tencent-Cloud.coding-copilot 并找 JWT |
| `_extract/dump_keys.py` | dump_keys.py | 遍历 state.vscdb 全部键值 dump |
| `_extract/dump_settings.py` | dump_settings.py | 转储 CodeBuddy.settings 与 coding-copilot 值 |
| `_extract/decrypt_all.py` | decrypt_all.py | 用 DPAPI/AES 解密全部 secret（含 AESGCM 键推导） |
| `_extract/dpapi_unpack.py` | dpapi_unpack.py | 对 secret blob 做 DPAPI CryptUnprotectData 解包 |
| `_extract/extract2.py` | extract2.py | 遍历并 DPAPI 解密 accessToken secret blob |
| `_extract/extract_session.py` | extract_session.py | AES-GCM 解密并还原会话对象 |
| `_extract/crack_v10.py` | crack_v10.py | 推导 v10 加密键并 AES 解密 secret blob |
| `_extract/refresh_session.py` | refresh_session.py | 用 refreshToken 请求刷新并更新会话 |
| `_extract/list_paths.py` | list_paths.py | 正则提取 index.js 中全部 /v2/ 路径 |
| `_extract/list_v2_paths.py` | list_v2_paths.py | 提取 /v2/ 及 completions 相关 URL 子串 |

## 已组织脚本（未移动，仅登记）

| 新路径（相对 src-true） | 原文件名 | 用途 |
|---|---|---|
| `test-run/test_alias.py` | test_alias.py | 通过网关测试 deepseek-chat 别名 |
| `test-run/test_stream_alias.py` | test_stream_alias.py | 通过网关测试 claude-3-5-sonnet 流式别名 |
| `tools/mail_code_server.py` | mail_code_server.py | QQ 邮箱 IMAP 验证码侦测与 Key 自动保存服务 |