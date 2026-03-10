# OneKeyVE

## 仓库说明

这是 `OneKeyVE` 的仓库根目录。

当前仓库包含三部分：

- `OneKeyVE_GO/`：当前主用的 Go 版本
- `OneKeyVE_ALL/`：历史 Python 版本与旧资料
- `test/`：测试视频和测试输出目录

如果你要直接使用程序，优先看 `OneKeyVE_GO/`。

## 快速开始

普通版 GUI：

```powershell
F:\Project\OneKey_VE\OneKeyVE_GO\bin\OneKeyVE.exe
```

完整内嵌版 GUI：

```powershell
F:\Project\OneKey_VE\OneKeyVE_GO\bin\OneKeyVE-embedded.exe
```

说明：

- `OneKeyVE.exe` 适合本机已有 FFmpeg 环境
- `OneKeyVE-embedded.exe` 适合没有 FFmpeg 环境的机器

## 目录用途

### `OneKeyVE_GO/`

当前正式实现目录。

这里包含：

- Go CLI
- Windows GUI
- 打包脚本
- 图标资源
- 构建与使用文档

详细说明见：

- [OneKeyVE_GO/README.md](/f:/Project/OneKey_VE/OneKeyVE_GO/README.md)

### `OneKeyVE_ALL/`

历史 Python 版本目录。

这里主要用于：

- 保留原始脚本
- 查阅旧需求和旧文档
- 对照历史处理逻辑

### `test/`

测试目录。

这里主要用于：

- 放测试视频
- 放测试输出
- 做功能验收

## 当前约束

- 允许使用 `C:\ffmpeg` 中的组件
- 禁止把处理结果写入 `C:` 盘
- Go 版本不复用 Python 代码、脚本、配置或构建资源

## Git 说明

仓库当前采用“本地保留大文件，但 Git 跳过跟踪”的方式。

根目录 [.gitignore](/f:/Project/OneKey_VE/.gitignore) 已排除：

- `OneKeyVE_GO/.gocache/`
- `OneKeyVE_GO/.gomodcache/`
- `OneKeyVE_GO/.tmp-go/`
- `OneKeyVE_GO/bin/`
- `OneKeyVE_ALL/temp/`
- `OneKeyVE_ALL/ffmpeg.rar`

这意味着这些文件可以继续保留在本地，但不会被提交到远程仓库。
