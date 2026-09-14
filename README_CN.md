# CLIProxyAPI Lite

[gchigoo](https://github.com/gchigoo) 维护的 CLIProxyAPI 精简分支，基于 [官方项目](https://github.com/router-for-me/CLIProxyAPI)，保留 MIT 许可证及上游 Git 历史。

目标是保留已有提供方能力和个人优化，删除动态插件系统，不引入 Devin，并选择性吸收上游对现有功能的修复与性能改进。

- 保留内置提供方、OAuth、账户轮换、冷却重试、模型别名、流式响应、WebSocket、图片处理、管理接口、配置热更新和内置用量统计。
- 保留账户稳定的原生身份、TLS/uTLS、显式代理失败时禁止直连、Gemini/Vertex 请求头和 Codex 多代理客户端兼容。
- 删除动态插件加载、插件 SDK／商店／安装更新、插件路由和 OAuth、Home 插件同步，以及请求、响应、转换和调度中的动态插件钩子。
- Home 非插件功能、存储后端、TUI 和内置 Redis 用量统计仍保留。内置用量统计接口中的 `Plugin` 名称不代表可加载第三方代码。

初始基础版本为 **v7.2.156**，吸收了截至 **v7.3.2 的 33 个选定改动**，不等同于完整的官方 v7.3.2。

## 构建与运行

需要 Go 1.26 或更高版本，无需 C 编译工具链。

```powershell
$env:CGO_ENABLED = "0"
go build -trimpath -ldflags="-s -w" -o cli-proxy-api.exe ./cmd/server
Copy-Item config.example.yaml config.yaml
```

编辑 `config.yaml`，配置实际需要的提供方后运行：

```powershell
.\cli-proxy-api.exe -config config.yaml
```

Linux 构建命令和详细说明见 [README.md](README.md)。Docker 使用本地源码构建：`docker compose up -d --build`，默认不会拉取官方 CPA 镜像。

旧配置中的 `plugins:` 会被忽略，无法启用动态扩展，建议自行删除。插件专属管理和资源路由不存在，管理接口能力头返回 `X-CPA-SUPPORT-PLUGIN: 0`。

`-local-model` 使用内嵌模型目录；它不等于关闭所有后台元数据请求。

## 更新与验证

- [UPSTREAM_PATCHES.md](UPSTREAM_PATCHES.md)：上游改动及引入、适配、跳过原因。
- [CUSTOMIZATIONS.md](CUSTOMIZATIONS.md)：必须保留的个人优化。
- [MAINTENANCE.md](MAINTENANCE.md)：后续补丁维护流程。
- [VALIDATION.md](VALIDATION.md)：实际验证结果及测量条件。

不自动整版同步官方主分支。真实配置、OAuth／账户文件、API 密钥、日志和部署记录不进入公开仓库。测试和本地模拟请求不能替代真实提供方验证或生产部署验证。
