# Meridian Deck

Meridian Deck 是面向服务器运维与科研计算的 SSH 桌面工作台。在同一个窗口管理 SSH 会话和远程文件，也能查看服务器资源、GPU 状态和运行日志。

## 下载

**v1.0.0 正在准备中。** 正式发布后，可在 [GitHub Releases](https://github.com/LisonZhang0623/Meridian-Deck-Official/releases) 查看版本说明并下载对应平台的桌面包。

| 平台 | 计划提供的文件 | 运行方式 |
| --- | --- | --- |
| Windows x64 | `Meridian-Deck-1.0.0-x64.exe` | 双击运行便携 EXE。需要 [Microsoft Edge WebView2 Evergreen Runtime](https://developer.microsoft.com/microsoft-edge/webview2/)。 |
| macOS（Apple Silicon） | `Meridian-Deck-1.0.0-macos-arm64.zip` | 解压后打开 `Meridian Deck.app`。 |
| Linux x64 | `Meridian-Deck-1.0.0-linux-x64.tar.gz` | 解压后在包内目录运行 `./Meridian-Deck`。需要 GTK 4 和 WebKitGTK 6.0。 |

## 主要功能

- **SSH 终端**支持多标签、分屏、输出搜索和断线重连，也可接入远程 tmux 会话。
- **连接管理**支持分组、标签和最近连接。可使用密码、私钥、SSH Agent 或交互式认证，也可通过多跳跳板机连接。
- **SFTP 文件管理**支持浏览远程目录、上传、下载和编辑文本文件，可预览 Markdown、CSV／TSV 和图片。保存前会检查远程文件是否已被修改。
- **资源监控与日志**可查看远程 Linux 主机的 CPU、内存、磁盘和网络状态，通过服务器上的 `nvidia-smi` 查看 NVIDIA GPU 利用率、显存和温度，并实时追踪远程日志。
- **命令收藏与端口转发**支持保存带参数的常用命令，并配置本地 TCP、远程 TCP 和 SOCKS5 转发。

## 快速开始

1. 启动应用，选择 **连接 → 新建 SSH 连接**。
2. 输入服务器地址、端口、用户名和认证信息，选择 **保存并连接**。
3. 首次连接时，核对服务器主机密钥指纹，再选择 **信任并继续**。
4. 连接后即可使用终端，或从侧栏打开 **文件**、**显卡**、**资源**和**日志**面板。

## 问题反馈

欢迎在 [GitHub Issues](https://github.com/LisonZhang0623/Meridian-Deck-Official/issues) 提交问题或功能建议。报告问题时，请附上操作系统及架构、应用版本、复现步骤和相关报错。分享日志或截图前，请移除服务器地址、账号、密码、私钥和令牌。
