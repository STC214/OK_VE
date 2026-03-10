#!/usr/bin/env python3
"""
单步合成测试脚本 - 验证高分辨率羽化合成是否成功
输入: 任意视频 (如 test.mp4)
输出: output_test_1080x2736.mp4, output_test_1080x2400.mp4
"""

import sys
from pathlib import Path
import ffmpeg

# === 配置 ===
INPUT_VIDEO = "001.mp4"          # 👈 替换为你的测试视频
OUTPUT_DIR = Path("output_test")
FEATHER_WIDTH = 30

# 目标比例
TARGET_RATIOS = [
    (1080 / 2736, "1080x2736"),
    (1080 / 2400, "1080x2400")
]

def get_video_info(path):
    probe = ffmpeg.probe(str(path))
    stream = next(s for s in probe['streams'] if s['codec_type'] == 'video')
    return int(stream['width']), int(stream['height'])

def single_step_composite(input_path, output_path, target_w, target_h):
    print(f"🎬 合成 {target_w}x{target_h} -> {output_path}")
    
    input_vid = ffmpeg.input(str(input_path)).video

    # 1. 背景：缩放+裁剪+高斯模糊
    bg = (
        input_vid
        .filter('scale', w=target_w, h=target_h, force_original_aspect_ratio='increase')
        .filter('crop', target_w, target_h)
        .filter('gblur', sigma=15)
    )

    # 2. 前景：按比例缩放 + 居中填充 + 羽化 Alpha
    orig_w, orig_h = get_video_info(input_path)
    orig_ratio = orig_w / orig_h
    target_ratio = target_w / target_h

    if orig_ratio > target_ratio:
        scale_w, scale_h = target_w, int(target_w / orig_ratio)
    else:
        scale_h, scale_w = target_h, int(target_h * orig_ratio)

    pad_x = (target_w - scale_w) // 2
    pad_y = (target_h - scale_h) // 2

    # 羽化 Alpha 表达式
    fw = FEATHER_WIDTH
    geq_a = (
        f"if(lt(X,{fw}), X/{fw}, if(gt(X,{target_w}-{fw}), ({target_w}-X)/{fw}, 1)) * "
        f"if(lt(Y,{fw}), Y/{fw}, if(gt(Y,{target_h}-{fw}), ({target_h}-Y)/{fw}, 1)) * 255"
    )

    fg = (
        input_vid
        .filter('scale', w=scale_w, h=scale_h)
        .filter('pad', w=target_w, h=target_h, x=pad_x, y=pad_y, color='black')
        .filter('geq', r='lum', g='lum', b='lum', a=geq_a)
    )

    # 3. 合成
    final = bg.overlay(fg)

    # 4. 输出（无音频）
    ffmpeg.output(final, str(output_path), vcodec='libx264', crf=20, an=None).run(overwrite_output=True)
    print(f"✅ 完成: {output_path}")

# === 主流程 ===
if __name__ == "__main__":
    input_file = Path(INPUT_VIDEO)
    if not input_file.exists():
        print(f"❌ 找不到输入文件: {input_file}")
        sys.exit(1)

    OUTPUT_DIR.mkdir(exist_ok=True)
    
    # 获取源宽（作为目标宽度）
    src_w, src_h = get_video_info(input_file)
    print(f"源视频尺寸: {src_w}x{src_h}")

    for ratio, name in TARGET_RATIOS:
        target_w = src_w
        target_h = round(src_w / ratio)
        output_path = OUTPUT_DIR / f"output_test_{name}.mp4"
        single_step_composite(input_file, output_path, target_w, target_h)

    print("🎉 所有测试输出完成！检查 output_test/ 目录")