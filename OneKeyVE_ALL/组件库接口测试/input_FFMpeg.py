import json
from pathlib import Path

# 加载诊断报告
with open("ffmpeg_full_diagnostics.json", "r", encoding="utf-8") as f:
    diag = json.load(f)

# === 实用示例 ===
# 1. 检查ffmpeg是否可用
if diag["components"]["ffmpeg"]["found"] and diag["components"]["ffmpeg"].get("status") == "operational":
    ffmpeg_path = diag["components"]["ffmpeg"]["path"]
    print(
        f"✓ FFmpeg可用: {ffmpeg_path} (v{diag['components']['ffmpeg'].get('version', '未知')})")

# 2. 检查是否支持硬件编码
hw_encoders = diag["components"].get("capabilities", {}).get(
    "encoders", {}).get("hardware", [])
if hw_encoders:
    print(f"💡 可用硬件编码器: {', '.join(hw_encoders)}")
    # 动态构建命令
    ffmpeg_path = diag["components"]["ffmpeg"]["path"]
    cmd = [ffmpeg_path, "-i", "input.mp4",
           "-c:v", hw_encoders[0], "output.mp4"]

# 3. 获取旋转处理命令范式
for paradigm in diag.get("command_paradigms", {}).get("ffmpeg", []):
    if paradigm["id"] == "rotate_video":
        if paradigm["compatibility"]["available"]:
            template = paradigm["command_template"]
            # 安全替换参数
            cmd = template.format(input="video.mp4", output="rotated.mp4")
            print(f"🔄 旋转命令: {cmd}")
        else:
            print("⚠️ 当前FFmpeg不支持transpose滤镜！")

# 4. 检查关键协议支持
protocols = diag.get("capabilities", {}).get("protocols", {})
if "rtmp" in protocols.get("output", []):
    print("✅ 支持RTMP推流")
