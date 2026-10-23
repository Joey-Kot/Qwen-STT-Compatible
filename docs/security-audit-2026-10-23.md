# 依赖安全审计（2026-10-23）

基线提交：`42b3f90147167c7ca6e3c393c87494ec5a7f9646`。

## 结论与更新

按项目原有 Go 1.26.4 构建配置扫描，`govulncheck` 报告 **17 条可达符号告警**、
4 条仅导入包匹配、3 条仅标准库版本匹配，全部来自 Go 标准库。
这是静态调用分析结果，没有逐条复现漏洞利用条件。

升级后的普通源码、`libav` 源码和 Linux 静态二进制扫描均无 Go 漏洞匹配。
FFmpeg 的 C 代码另外通过上游公告、实际构建配置、补丁和回归测试核对，
不依赖 `govulncheck` 判断。

| 组件 | 原版本 | 更新后 |
| --- | --- | --- |
| Go | 1.26.4 | 1.27.3；CI 从 `go.mod` 读取版本 |
| ASR-Audio-Preprocess | `v0.0.0-20260901092746-72922741cf43` | `v0.0.0-20261023044719-82196363aea0` |
| 静态 FFmpeg | 8.1.2 | 8.1.3 + ASR 上游提供的 CVE-2026-107678 补丁 |
| Gorilla WebSocket | v1.5.3 | `v1.5.4-0.20240701034025-d67f41855da4`，固定到掩码随机数修复提交 |
| `golang.org/x/net` | 无独立模块依赖 | v0.61.0；新版 WebSocket 的代理支持需要 |
| Opus | 1.5.2 | 保持 1.5.2；本次未发现适用的已公开 CVE |

按要求移除了 `Dockerfile`、`.dockerignore` 和 Docker 运行环境说明。
运行环境中独立安装的 FFmpeg 仍需另行处理，见下文。

## Go 漏洞匹配

扫描器为 `govulncheck v1.9.0`，数据库为 `https://vuln.go.dev`，
数据库更新时间为 **2026-10-21 08:27:29（北京时间）**。
基线显式使用 `GOTOOLCHAIN=go1.26.4`，避免本机已更新的 Go 版本掩盖旧构建的告警。

| Go 公告 | CVE | 基线匹配级别 |
| --- | --- | --- |
| [GO-2026-5026](https://pkg.go.dev/vuln/GO-2026-5026) | CVE-2026-39821 | 可达符号 |
| [GO-2026-5856](https://pkg.go.dev/vuln/GO-2026-5856) | CVE-2026-42505 | 可达符号 |
| [GO-2026-5972](https://pkg.go.dev/vuln/GO-2026-5972) | CVE-2026-33818 | 可达符号 |
| [GO-2026-6088](https://pkg.go.dev/vuln/GO-2026-6088) | CVE-2026-56859 | 可达符号 |
| [GO-2026-6089](https://pkg.go.dev/vuln/GO-2026-6089) | CVE-2026-56853 | 可达符号 |
| [GO-2026-6090](https://pkg.go.dev/vuln/GO-2026-6090) | CVE-2026-56862 | 可达符号 |
| [GO-2026-6218](https://pkg.go.dev/vuln/GO-2026-6218) | CVE-2026-56860 | 可达符号 |
| [GO-2026-6603](https://pkg.go.dev/vuln/GO-2026-6603) | CVE-2026-78659 | 可达符号 |
| [GO-2026-6605](https://pkg.go.dev/vuln/GO-2026-6605) | CVE-2026-56866 | 可达符号 |
| [GO-2026-6607](https://pkg.go.dev/vuln/GO-2026-6607) | CVE-2026-97031 | 可达符号 |
| [GO-2026-6608](https://pkg.go.dev/vuln/GO-2026-6608) | CVE-2026-94440 | 可达符号 |
| [GO-2026-6610](https://pkg.go.dev/vuln/GO-2026-6610) | CVE-2026-78660 | 可达符号 |
| [GO-2026-6611](https://pkg.go.dev/vuln/GO-2026-6611) | CVE-2026-78669 | 可达符号 |
| [GO-2026-6612](https://pkg.go.dev/vuln/GO-2026-6612) | CVE-2026-78663 | 可达符号 |
| [GO-2026-6613](https://pkg.go.dev/vuln/GO-2026-6613) | CVE-2026-94439 | 可达符号 |
| [GO-2026-6617](https://pkg.go.dev/vuln/GO-2026-6617) | CVE-2026-97032 | 可达符号 |
| [GO-2026-6809](https://pkg.go.dev/vuln/GO-2026-6809) | CVE-2026-111943 | 可达符号 |
| [GO-2026-4970](https://pkg.go.dev/vuln/GO-2026-4970) | CVE-2026-39822 | 仅导入包 |
| [GO-2026-5942](https://pkg.go.dev/vuln/GO-2026-5942) | CVE-2026-46600 | 仅导入包 |
| [GO-2026-6604](https://pkg.go.dev/vuln/GO-2026-6604) | CVE-2026-56857 | 仅导入包 |
| [GO-2026-6609](https://pkg.go.dev/vuln/GO-2026-6609) | CVE-2026-78667 | 仅导入包 |
| [GO-2026-6091](https://pkg.go.dev/vuln/GO-2026-6091) | CVE-2026-56858 | 仅标准库版本 |
| [GO-2026-6599](https://pkg.go.dev/vuln/GO-2026-6599) | CVE-2026-94448 | 仅标准库版本 |
| [GO-2026-6600](https://pkg.go.dev/vuln/GO-2026-6600) | CVE-2026-97030 | 仅标准库版本 |

主要涉及 HTTP/1、HTTP/2、TLS、证书校验、MIME、XML、ASN.1 和 URL 处理。
Go 1.27.3 包含最近的 CVE-2026-111943 证书信任修复，详见
[Go 官方发布公告](https://groups.google.com/g/golang-announce/c/66pojueaQek)。

## Gorilla WebSocket 公告与源码不一致

[GO-2026-6278](https://pkg.go.dev/vuln/GO-2026-6278) /
[GHSA-w67g-5rqw-f597](https://github.com/canolgun/websocket/security/advisories/GHSA-w67g-5rqw-f597)
是未审核公告，没有对应 CVE 编号。其版本区间把 v1.5.3 列为已修复，
因此扫描器没有报告当前版本；但从校验过的模块源码可见，v1.5.3 的
`newMaskKey()` 仍调用 `math/rand.Uint32()`。

固定到公告引用的上游修复提交
[`d67f41855da4`](https://github.com/gorilla/websocket/commit/d67f41855da42d7bccd9ef050c49f7e54e783b95)，
核实该实现使用 `crypto/rand`。使用精确伪版本，避免把 v1.5.3 的数据库匹配结果
当作修复存在的证据。新引入的 `golang.org/x/net` 同步固定到 v0.61.0，
并补充许可证。

## FFmpeg 与构建集成

静态构建启用了 MOV 解封装，原 FFmpeg 8.1.2 会处理恶意 MP4/M4A 文件中的
`pssh` 元数据，适用
[CVE-2026-107678](https://security-tracker.debian.org/tracker/CVE-2026-107678)。
普通 8.1.3 发布包本身仍需要
[FFmpeg PR 24593](https://code.ffmpeg.org/FFmpeg/FFmpeg/pulls/24593) 的补丁。
最新版 ASR 已携带该补丁，本项目继续委托其构建和验证，不复制维护 C 补丁。

本项目原脚本显式导出 `FFMPEG_VERSION=8.1.2`，会覆盖 ASR 新默认值；
现已对齐为 8.1.3，并对齐 FFmpeg、Opus 的默认 SHA-256。
构建时验证了 FFmpeg 发布签名及固定指纹
`FCF986EA15E6E293A5644F10B4322F04D67658D8`。

Go 模块缓存中的脚本没有可执行位，而 ASR 新脚本直接调用补丁辅助脚本。
新增的项目包装脚本将固定版本的上游脚本和补丁复制到临时目录，恢复脚本执行权限，
构建后清理；原模块缓存不变。已测试两个构建入口、错误码传递、包含空格的路径、
原文件内容和权限保持不变及临时目录清理。

核对实际生成的 FFmpeg 配置：MOV、`asplit` 已启用；IAMF、H.264 解码器未启用。
其他原生组件的逐项筛选沿用该固定 ASR 版本的
[安全审计](https://github.com/Joey-Kot/ASR-Audio-Preprocess/blob/82196363aea078a9eea6a53796f74e2ebc56268e/docs/security-audit-2026-10-23.md)，
并复用了当日 NVD 查询素材；Opus 1.5.2 的对应产品/版本查询为零条。

## 独立 FFmpeg 的剩余事项

`internal/realtime/audio.go` 的实时文件解码通过 `PATH` 启动独立的 `ffmpeg`。
更新 ASR 静态库和移除 Docker 均不会更新这个程序。

本机检测到 `ffmpeg 8.0.1-3ubuntu2`。审计时
[Ubuntu 跟踪页](https://ubuntu.com/security/CVE-2026-107678) 对该漏洞仍标为
“Needs evaluation”，尚不能确认本机程序携带对应修复。
使用实时文件接口的部署需要单独安装带修复的 FFmpeg；本次未修改系统包。
持续 PCM 的 WebSocket 接口不调用该程序。

## 验证

在 Linux amd64 / Go 1.27.3 上完成：

- `go test ./...`、`go vet ./...`、`go test -tags libav -race ./...` 通过。
- 普通源码、`-tags libav` 源码和构建后二进制的 `govulncheck` 均无匹配。
- `go mod verify` 通过；二进制构建信息确认了上述最终版本。
- 两个原生依赖构建入口通过；重复构建识别已应用的补丁。
- FFmpeg `fate-encryption-info` 通过；MOV 回归测试通过，验证两个音轨中
  32,768 + 7 条 `pssh` 记录的数量、内容及顺序。
- Linux 静态二进制构建及 `--help` 启动检查通过。
- 新增真实 CONNECT 隧道回归测试，无认证和带认证两种 WebSocket 代理连接均通过。
- `actionlint`、Shell 语法、Go 格式及 Git 空白检查通过。

上游 WebSocket 自带的 `TestProxyDial`、`TestProxyAuthorizationDial` 两项测试
在 Go 1.27.3 下失败；旧 v1.5.3 在相同工具链下也失败，而旧版本配 Go 1.26.4 时通过。
这两项模拟服务器在 CONNECT 返回 2xx 后继续将同一连接作为普通 HTTP 连接处理，
没有接管连接建立隧道。项目新增测试使用真实隧道验证了实际代理行为；上游其余测试通过。
项目测试及 CI 没有跳过这类应用行为检查。

构建仍有上游 C `sample_fmts` 弃用和 glibc 静态链接提示。
Windows、macOS 和 ARM64 的构建未在本机执行，交由现有 CI 矩阵验证。
这些结论限于当前公告数据库、固定依赖、已启用原生功能和上述实际验证范围。
