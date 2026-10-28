# tui-copier

[![CI](https://github.com/liking008/tui-copier/actions/workflows/ci.yml/badge.svg)](https://github.com/liking008/tui-copier/actions/workflows/ci.yml)
[![License: GPL-3.0](https://img.shields.io/badge/License-GPL--3.0-blue.svg)](LICENSE)

FreeKill 资源拷贝与语音下载的终端界面工具，使用 Go + [Bubble Tea](https://github.com/charmbracelet/bubbletea) 编写。

原项目位于 FreeKill 仓库的 `tools/tui-copier`，这里独立发布，方便构建、下载和跟踪改动。

## 功能

- **原画拷贝**：在整合包之间复制武将原画
- **阵亡语音拷贝**：复制阵亡语音
- **技能语音拷贝**：复制技能语音
- **语音下载**：按 URL 下载语音文件并放到指定包
- **技能文件拷贝**：复制技能 Lua 文件
- **测试技能**：调用 FreeKill 的 `-testfile` 运行技能测试并汇总输出
- **快速启动配置**：写入 `quick_start.json` 并可直接启动 `FreeKill -q`
- 模糊搜索、候选补全、表单校验、操作确认与结果展示

## 环境要求

- Go 1.24 或更高版本（从源码构建时）
- 一个可运行的 FreeKill 目录，且包含 `packages/` 子目录
- 支持 ANSI 的终端；Windows 建议使用 Windows Terminal

## 下载

从 [Releases](https://github.com/liking008/tui-copier/releases/latest) 下载对应平台的可执行文件：

| 平台 | 文件 |
|---|---|
| Linux x86_64 | `tui-copier-linux-amd64` |
| Linux ARM64 | `tui-copier-linux-arm64` |
| Windows x86_64 | `tui-copier-windows-amd64.exe` |
| macOS Apple Silicon | `tui-copier-darwin-arm64` |

下载后赋予执行权限：

```sh
chmod +x tui-copier-linux-amd64
```

## 使用

`tui-copier` 必须 **在 FreeKill 项目根目录运行**，因为它会读取当前目录下的 `packages/`：

```sh
cd /path/to/FreeKill
/path/to/tui-copier-linux-amd64
```

如果当前目录没有 `packages/`，程序会提示：

```text
Error: packages/ directory not found in ...
Please run this tool from the FreeKill project root.
```

### 快捷键

| 按键 | 作用 |
|---|---|
| `↑` / `↓` | 移动选择 / 浏览候选 |
| `Enter` | 确认、提交当前步骤 |
| `Esc` / `q` | 返回上一级或退出 |
| `Tab` | 切换输入焦点 |
| `Ctrl+S` | 提交表单 |
| `Ctrl+A` | 切换全选 / 候选操作 |

## 从源码构建

```sh
git clone https://github.com/liking008/tui-copier.git
cd tui-copier
go build -trimpath -ldflags "-s -w" -o tui-copier .
```

交叉编译示例：

```sh
GOOS=linux   GOARCH=arm64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o tui-copier-linux-arm64 .
GOOS=windows GOARCH=amd64 CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o tui-copier-windows-amd64.exe .
```

## 测试

```sh
go test ./...
```

## 说明

- 本工具会直接修改 FreeKill 的 `packages/`，建议操作前备份或在 Git 仓库中运行，便于回滚。
- 语音下载功能会访问你填写的 URL，请只使用可信来源。
- 本项目源自 FreeKill（GPL-3.0），按 GPL-3.0 发布。

## 许可证

[GPL-3.0](LICENSE)
