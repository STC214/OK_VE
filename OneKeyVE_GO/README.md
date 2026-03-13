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

- 递归扫描工作目录及其子目录中的视频文件
- 即使根目录没有视频文件，只要子目录有视频文件也可以正常处理
- 读取视频宽高和帧数
- 横屏输入自动转入竖屏处理流程
- 输出两套目标比例
- `9x20`
- `5x11`
- 背景模糊 + 前景羽化滤镜链
- 默认使用 `h264_nvenc`
- 不可用时回退 `libx264`
- 黑边检测后单次 `crop`

## 视频发现与输出规则

- 根目录视频输出到“根目录输出”下的 `<比例名>` 目录
- 子目录视频输出到视频所在目录下的 `<比例名>` 目录
- 当“根目录输出”和“工作目录”相同时，程序仍会保留正常源视频，只跳过自己生成的比例目录
- 扫描时会自动跳过程序已生成的根目录输出和子目录比例输出，避免重复处理

## GUI 现状

当前 GUI 为 Windows 原生单窗口界面，已实现：

- 浏览目录和选择文件时不弹出 `cmd` 或 `powershell`
- 开始处理后调用 `ffmpeg` 和 `ffprobe` 时不弹出控制台窗口
- 进度条按实际任务进度更新
- 日志实时刷新，最新日志显示在最上面
- 处理在后台线程执行，界面不应被主流程直接阻塞
- 运行中支持暂停、继续和停止当前任务
- 当前配置会定时自动保存，并在关闭窗口时再做一次最终保存

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
- `ONEKEYVE_BLACK_MODE`
- `ONEKEYVE_BLACK_THRESHOLD`
- `ONEKEYVE_BLACK_RATIO`
- `ONEKEYVE_BLACK_RUN`
- `ONEKEYVE_RUNTIME_DIR`
- `ONEKEYVE_CACHE_DIR`

## 黑边检测策略

当前黑边检测逻辑为：

- 默认模式为 `center_crop`
- 备选模式为 `legacy`
- 仅分析视频开始后的第 `1` 秒
- 等间隔采样 `4` 帧并取平均结果
- 从画面中心向上下左右寻找黑边
- 当某一行或某一列在有效检测带中黑像素占比达到 `60%` 以上时，视为黑线
- 需要至少连续 `2` 条黑线才认定为边界
- 探测到黑线后只执行一次 `crop`
- `crop` 坐标向内取整，确保黑线本身不会被保留在输出中

`legacy` 备选模式保留的是旧策略整条链路，不只是单帧判定：

- 先检查前 `3` 秒是否完全无黑边，可提前退出
- 从 `0.5` 秒开始采样，最多取 `8` 帧
- 采样结果使用中位数汇总
- 继续做侧边持久化黑边分析
- 最终使用成对、对称的旧式裁切规则

当前默认参数：

- 黑线阈值：`6`
- 黑像素占比：`60`
- 连续黑线数：`2`

CLI 可覆盖这些参数：

```powershell
go run .\cmd\onekeyve --encoder h264_nvenc --black-mode center_crop --black-threshold 6 --black-ratio 60 --black-run 2
```

## GUI 配置保存

GUI 会每 `30` 秒把当前配置自动保存到 EXE 所在目录：

- `onekeyve_gui_config.json`

另外：

- 关闭窗口前会立即再保存一次当前配置
- 如果保存失败，GUI 会弹出警告，并把失败信息写入日志

保存内容包括：

- 工作目录
- 输出目录
- FFmpeg / FFprobe 路径
- 编码器选项
- 去黑边模式
- 模糊和羽化参数

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
- 根目录没有视频、仅子目录有视频时可正常发现并处理
- “根目录输出”和“工作目录”相同时不会再把整个工作目录误判为输出目录
- 内嵌版运行时日志会显示 `binary source: embedded`
- 横屏和竖屏样例都已实测
- `9x20` 和 `5x11` 两套输出均已成功生成
- 输出写入 `C:` 时会被程序拒绝
- GUI 中目录选择、文件选择和开始处理都不会弹出控制台窗口

## 待办

当前待办：

1. 视需要把黑线阈值、黑像素占比、连续黑线数接入 GUI
2. 增加更多真实样例回归测试

## 使用建议

- 本机开发和调试优先使用 `OneKeyVE.exe`
- 发给没有 FFmpeg 环境的机器时，优先使用 `OneKeyVE-embedded.exe`
