# OneKeyVE Go 版本

## 项目说明

`OneKeyVE_GO` 是仓库中的当前主实现。

仓库级导航和目录说明见：

- [根目录 README](/f:/Project/OneKey_VE/README.md)

当前已经完成：

- Go CLI 主流程重构
- Windows 原生 GUI
- 普通版 GUI 打包
- 完整内嵌 FFmpeg 运行时的 GUI 打包
- EXE 图标接入
- FFmpeg 多来源定位
- 禁止写入 `C:` 输出
- 真实样例视频验证

当前产物：

- `bin/OneKeyVE.exe`
- `bin/OneKeyVE-embedded.exe`

## 约束

- Go 版本不复用 Python 代码、脚本、配置或构建资源
- 允许读取 `C:\ffmpeg` 中的组件
- 禁止把输出结果写入 `C:` 盘
- 内嵌版运行时也遵循“不写入 C 盘”原则

## 目录结构

- `cmd/onekeyve`：CLI 入口
- `cmd/onekeyve-gui`：GUI 入口
- `internal/app`：批处理主流程
- `internal/ffmpeg`：FFmpeg、FFprobe、滤镜和定位逻辑
- `internal/gui`：Windows 原生 GUI
- `internal/procutil`：Windows 下无控制台子进程辅助逻辑
- `assets/app.ico`：程序图标
- `pack/build_gui.ps1`：普通版 GUI 打包脚本
- `pack/build_gui_embedded.ps1`：完整内嵌版 GUI 打包脚本
- `pack/convert_image_icon.ps1`：图片转 `ico`
- `pack/embed_icon.ps1`：把图标写入 EXE
- `pack/refresh_shell_icons.ps1`：刷新 Windows 图标缓存

## 功能范围

当前版本支持：

- 扫描工作目录下的视频文件
- 读取视频宽高和帧数
- 横屏输入自动转入竖屏处理流程
- 输出两套目标比例
- `9x20`
- `5x11`
- 背景模糊 + 前景羽化滤镜链
- 自动检测编码器
- 优先 `h264_nvenc`
- 回退 `libx264`

## GUI 现状

当前 GUI 为 Windows 原生单窗口界面，已实现：

- 浏览目录和选择文件时不弹出 `cmd` 或 `powershell`
- 开始处理后调用 `ffmpeg` 和 `ffprobe` 时不弹出控制台窗口
- 进度条按实际任务进度更新
- 日志实时刷新，最新日志显示在最上面
- 处理在后台线程执行，界面不应被主流程直接阻塞

## FFmpeg 查找顺序

当前查找顺序如下：

1. 命令行参数和显式环境变量
2. 内嵌运行时
3. EXE 所在目录下的 `ffmpeg` 文件夹
4. 各盘根目录下的 `ffmpeg` 文件夹
5. 工作目录扫描
6. 系统 `PATH`
7. 诊断文件兜底

支持的环境变量：

- `ONEKEYVE_FFMPEG`
- `ONEKEYVE_FFPROBE`
- `ONEKEYVE_WORKDIR`
- `ONEKEYVE_OUTPUT`
- `ONEKEYVE_OUTPUT_DIR`
- `ONEKEYVE_ENCODER`
- `ONEKEYVE_RUNTIME_DIR`
- `ONEKEYVE_CACHE_DIR`

## 内嵌版说明

`OneKeyVE-embedded.exe` 面向没有 FFmpeg 环境的机器。

它会把完整运行时打进 EXE，目前包含：

- `ffmpeg.exe`
- `ffprobe.exe`
- `ffplay.exe`
- `avcodec-62.dll`
- `avdevice-62.dll`
- `avfilter-11.dll`
- `avformat-62.dll`
- `avutil-60.dll`
- `swresample-6.dll`
- `swscale-9.dll`

运行时会优先使用内嵌 FFmpeg，并解包到非 `C:` 目录。

运行目录优先读取：

- `ONEKEYVE_RUNTIME_DIR`
- `ONEKEYVE_CACHE_DIR`
- `TMPDIR`
- `TEMP`
- `TMP`

如果这些路径都不可用，程序会继续尝试 EXE 所在目录和工作目录中的非 `C:` 路径。

## 构建

构建普通版 GUI：

```powershell
Set-Location F:\Project\OneKey_VE\OneKeyVE_GO
.\pack\build_gui.ps1
```

构建完整内嵌版 GUI：

```powershell
Set-Location F:\Project\OneKey_VE\OneKeyVE_GO
.\pack\build_gui_embedded.ps1
```

运行 CLI：

```powershell
Set-Location F:\Project\OneKey_VE\OneKeyVE_GO
$env:ONEKEYVE_WORKDIR = 'F:\Project\OneKey_VE\test'
$env:ONEKEYVE_OUTPUT = 'F:\Project\OneKey_VE\test\output-go'
$env:ONEKEYVE_FFMPEG = 'C:\ffmpeg\bin\ffmpeg.exe'
$env:ONEKEYVE_FFPROBE = 'C:\ffmpeg\bin\ffprobe.exe'
go run .\cmd\onekeyve
```

## 验证状态

当前已经验证：

- `go test ./...` 通过
- 普通版 GUI 可正常启动
- 完整内嵌版 GUI 可正常启动
- 内嵌版运行时日志会显示 `binary source: embedded`
- 横屏和竖屏样例都已实测
- `9x20` 和 `5x11` 两套输出均已成功生成
- 输出写入 `C:` 时会被程序拒绝
- GUI 中目录选择、文件选择和开始处理都不会弹出控制台窗口

## 使用建议

- 本机开发和调试优先使用 `OneKeyVE.exe`
- 发给没有 FFmpeg 环境的机器时，优先使用 `OneKeyVE-embedded.exe`
