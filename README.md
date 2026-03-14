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
- 当前 Go 版本支持递归扫描工作目录及子目录中的视频
- 即使根目录没有视频，只要子目录有视频也可以正常处理
- 默认会把输出视频码率控制为输入视频码率的 `1.5` 倍
- 如果输入视频码率无法读取，则回退到程序内置编码参数

## 使用说明

### 使用前准备

- 当前主要面向 Windows 环境使用
- 如果机器上已经有可用的 FFmpeg / FFprobe，优先使用 `OneKeyVE.exe`
- 如果机器上没有 FFmpeg 环境，优先使用 `OneKeyVE-embedded.exe`
- 输出目录不要放在 `C:` 盘，程序会拒绝写入 `C:` 盘

### GUI 使用步骤

1. 运行 `OneKeyVE_GO/bin/OneKeyVE.exe` 或 `OneKeyVE_GO/bin/OneKeyVE-embedded.exe`
2. 在界面中选择“工作目录”，程序会递归扫描该目录及其子目录中的视频
3. 选择“输出目录”
4. 如果使用普通版 `OneKeyVE.exe`，且程序没有自动找到 FFmpeg / FFprobe，再手动指定它们的路径
5. 按需选择编码器、去黑边模式和其他处理参数
6. 点击开始处理，等待日志和进度条完成

运行中支持：

- 暂停
- 继续
- 停止当前任务

### CLI 使用示例

如果你想在命令行里直接运行当前 Go 版本，可以这样使用：

```powershell
Set-Location F:\Project\OneKey_VE\OneKeyVE_GO
$env:ONEKEYVE_WORKDIR = 'F:\Project\OneKey_VE\test'
$env:ONEKEYVE_OUTPUT = 'F:\Project\OneKey_VE\test\output-go'
$env:ONEKEYVE_FFMPEG = 'C:\ffmpeg\bin\ffmpeg.exe'
$env:ONEKEYVE_FFPROBE = 'C:\ffmpeg\bin\ffprobe.exe'
go run .\cmd\onekeyve
```

### 处理结果说明

- 根目录中的视频会输出到“根目录输出”下的对应比例目录
- 子目录中的视频会输出到视频所在目录下的对应比例目录
- 程序会自动跳过自己生成过的比例输出目录，避免重复扫描
- 如果目标输出已存在且体积不小于原视频，会直接跳过
- 如果目标输出存在但体积异常偏小，会删除后重新处理

更完整的参数、GUI 行为和构建说明见：

- [OneKeyVE_GO/README.md](/f:/Project/OneKey_VE/OneKeyVE_GO/README.md)

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

当前处理规则简述：

- 根目录视频输出到“根目录输出”下的 `<比例名>` 目录
- 子目录视频输出到视频所在目录下的 `<比例名>` 目录
- 扫描时会自动跳过程序自己生成的比例输出目录，避免重复处理
- 已有有效输出会跳过，过小的旧输出会删除后重做

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
