# OneKeyVE Python 版本

本目录保留的是历史 Python 版本代码。

当前目录定位：

- `ve_wallpaper_double_release.py` 是仓库中最接近最终交付逻辑的 Python 脚本。
- 其余 Python 脚本主要是历史阶段版本和试验版本。
- 本目录不再承载 Go 版本实现。

Go 版本已迁移到项目根目录下的独立子项目：

- `F:\Project\OneKey_VE\OneKeyVE_GO`

迁移原则：

- Go 版本与 Python 版本完全解耦。
- Go 版本不复用 Python 代码、脚本、配置和构建资源。
- 仅在后续确有需要时允许单独复用图标资源。

当前约束：

- 允许读取 `C:\ffmpeg` 中的 FFmpeg 组件。
- 坚决禁止把输出结果写入 `C:` 盘。

如果要继续维护 Python 版本，请以本目录内脚本为准。
如果要构建或运行 Go 版本，请进入 `OneKeyVE_GO`。
