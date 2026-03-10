# 审计说明

本文件保留对 Python 版本的审计结论，不再作为 Go 版本说明文档。

结论摘要：

1. `OneKeyVE_ALL` 内存在多个历史 Python 脚本，版本分叉严重。
2. Python 版本存在路径依赖、错误处理不足和编码器硬编码等问题。
3. 这些问题已经作为 Go 重构的来源背景，但 Go 实现现已迁移到独立目录。

当前目录职责：

- 保留 Python 历史实现
- 保留历史需求和审计背景
- 不再混放 Go 代码和 Go 构建产物

Go 版本位置：

- `F:\Project\OneKey_VE\OneKeyVE_GO`

运行约束仍然一致：

- 允许使用 `C:\ffmpeg\bin\ffmpeg.exe`
- 允许使用 `C:\ffmpeg\bin\ffprobe.exe`
- 不允许把输出写入 `C:` 盘
