"""
用python程序完成需求，要求使用纯FFmpeg实现，程序的异常处理能力和鲁棒性都要考虑到：
0. 视频文件可能是1个或者多个，所以是处理批量视频文件。当前程序的文件名是Video_Edit_FF.py（含后缀），我希望直接运行：python Video_Edit_FF.py 即可自动完成所有功能，而不是需要我额外的添加视频文件的文件名才可以。
1. 判断视频文件比例是否为9:16或者16:9，如果不是则进行2。如果是则跳过此视频文件的处理。
2. 我们将视频比例如9:16看作是一个数学上的分数，如9:16就是分数9/16，
   如果视频比例大于9/16则进行3，如果视频比例小于9/16则进行4。
3. 新建一个临时视频，画幅设定为9：16，画幅分辨率中的宽度同原视频的宽度。然后进行5。
4. 新建一个临时视频，画幅设定为9：16，画幅分辨率中的高度同原视频的高度。然后进行5。
5. 将原视频拷贝到临时视频中，拷贝过来的原视频的中心点应当和画幅的中心点重合，此时形成轨道01，轨道01以中心点不变等比例放大100%。
裁切掉超出画幅的部分以节省文件体积。对轨道01进行高斯模糊，模糊参数就用常用值即可。
再次拷贝原视频到临时视频中，同样拷贝过来的原视频的中心点应当和画幅的中心点重合，形成轨道02.
临时视频的轨道02导入原视频之后，正确的状态是原视频的中心点和临时视频的画幅的中心点重合。轨道02无需进行任何缩放操作。由于轨道02上的视频在此处理逻辑中必然会导致填不满宽度或者高度其中一个维度，这些填不满的部分会默认用黑色填充，将这些用黑色填充的部分裁切掉，这样轨道02裁切掉的部分就可以正常显示出轨道01的内容。
之后对裁切后的轨道02边缘做羽化处理，羽化值为常用值。
6. 保证所有轨道可见。合成视频到当前目录下的output目录（若没有则新建），
   导出的视频文件名为原视频文件名。

99.以上流程处理完之后，检查格式错误，检查语法错误，检查拼写错误，检查调用错误，修改完成后给我完整的代码。

999.加上详细注释

"""



import os
import subprocess
import sys
import json
import math
import glob
import shlex
import logging
from pathlib import Path

# 配置日志
logging.basicConfig(
    level=logging.INFO,
    format='%(asctime)s - %(levelname)s - %(message)s',
    handlers=[
        logging.FileHandler("video_edit.log"),
        logging.StreamHandler()
    ]
)
logger = logging.getLogger(__name__)


def check_ffmpeg():
    """检查ffmpeg和ffprobe是否可用"""
    try:
        subprocess.run(['ffmpeg', '-version'], stdout=subprocess.DEVNULL,
                       stderr=subprocess.DEVNULL, check=True)
        subprocess.run(['ffprobe', '-version'], stdout=subprocess.DEVNULL,
                       stderr=subprocess.DEVNULL, check=True)
        return True
    except (subprocess.CalledProcessError, FileNotFoundError):
        logger.error("FFmpeg或FFprobe未安装或不在系统路径中。请先安装FFmpeg。")
        return False


def get_video_info(file_path):
    """获取视频信息，包括分辨率和时长"""
    try:
        cmd = [
            'ffprobe',
            '-v', 'error',
            '-select_streams', 'v:0',
            '-show_entries', 'stream=width,height,duration',
            '-of', 'json',
            file_path
        ]
        result = subprocess.run(
            cmd, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True, check=True)
        info = json.loads(result.stdout)
        stream_info = info['streams'][0]
        return {
            'width': int(stream_info['width']),
            'height': int(stream_info['height']),
            'duration': float(stream_info.get('duration', 0))
        }
    except (subprocess.CalledProcessError, json.JSONDecodeError, KeyError, ValueError) as e:
        logger.error(f"获取视频信息失败 '{file_path}': {str(e)}")
        return None


def is_valid_aspect_ratio(width, height):
    """检查是否为9:16或16:9比例"""
    ratio = width / height if height != 0 else 0
    # 9:16 = 0.5625, 16:9 = 1.777...
    target_ratio_9_16 = 9 / 16
    target_ratio_16_9 = 16 / 9
    # 允许一些浮动误差
    tolerance = 0.01
    return (abs(ratio - target_ratio_9_16) < tolerance) or (abs(ratio - target_ratio_16_9) < tolerance)


def process_video(input_path, output_path, width, height):
    """处理不符合比例的视频"""
    try:
        # 计算比例
        ratio = width / height
        target_ratio = 9 / 16  # 9:16比例

        # 确定新画布尺寸
        if ratio > target_ratio:
            # 宽度为主
            new_width = width
            new_height = int(width / target_ratio)
        else:
            # 高度为主
            new_height = height
            new_width = int(height * target_ratio)

        # 确保尺寸为偶数 (FFmpeg要求)
        new_width = new_width if new_width % 2 == 0 else new_width + 1
        new_height = new_height if new_height % 2 == 0 else new_height + 1

        # 构建FFmpeg命令
        # 重新设计滤镜链:
        # 1. 背景层(轨道01)放大并模糊
        # 2. 前景层(轨道02)保持原比例，不强制缩放，直接居中放置
        cmd = [
            'ffmpeg',
            '-y',  # 覆盖输出文件
            '-i', input_path,
            '-filter_complex',
            f'[0:v]scale={new_width}:{new_height}:force_original_aspect_ratio=increase,crop={new_width}:{new_height}[bg_raw];'
            f'[bg_raw]gblur=sigma=20[bg];'
            f'[0:v]scale=w={new_width}:h={new_height}:force_original_aspect_ratio=decrease[fg];'
            f'[bg][fg]overlay=(main_w-overlay_w)/2:(main_h-overlay_h)/2[out]',
            '-map', '[out]',
            '-map', '0:a?',  # 如果有音频则复制
            '-c:v', 'libx264',
            '-preset', 'medium',
            '-crf', '23',
            '-c:a', 'aac',
            '-b:a', '128k',
            output_path
        ]

        logger.info(f"执行命令: {' '.join([shlex.quote(arg) for arg in cmd])}")

        # 执行命令
        process = subprocess.Popen(
            cmd,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            universal_newlines=True
        )

        # 读取stderr以显示进度
        while True:
            line = process.stderr.readline()
            if not line and process.poll() is not None:
                break
            if line:
                if "time=" in line:
                    logger.info(
                        f"处理中: {os.path.basename(input_path)} - {line.strip()}")

        # 检查返回码
        if process.returncode != 0:
            stdout, stderr = process.communicate()
            logger.error(f"FFmpeg处理失败 '{input_path}':\n{stderr}")
            return False

        logger.info(f"成功处理: '{input_path}' -> '{output_path}'")
        return True
    except Exception as e:
        logger.error(f"处理视频时出错 '{input_path}': {str(e)}")
        return False


def main():
    """主函数"""
    logger.info("开始视频处理...")

    # 检查FFmpeg
    if not check_ffmpeg():
        sys.exit(1)

    # 获取当前目录
    current_dir = os.getcwd()
    logger.info(f"当前工作目录: {current_dir}")

    # 创建output目录
    output_dir = os.path.join(current_dir, "output")
    os.makedirs(output_dir, exist_ok=True)
    logger.info(f"输出目录: {output_dir}")

    # 支持的视频扩展名
    video_extensions = ['.mp4', '.avi', '.mov',
                        '.mkv', '.flv', '.wmv', '.webm']

    # 获取所有视频文件
    video_files = []
    for ext in video_extensions:
        video_files.extend(glob.glob(os.path.join(current_dir, f'*{ext}')))
        video_files.extend(
            glob.glob(os.path.join(current_dir, f'*{ext.upper()}')))

    if not video_files:
        logger.warning("未找到视频文件。支持的格式: " + ", ".join(video_extensions))
        sys.exit(0)

    logger.info(f"找到 {len(video_files)} 个视频文件进行处理")

    processed_count = 0
    skipped_count = 0
    error_count = 0

    # 处理每个视频文件
    for video_file in video_files:
        file_name = os.path.basename(video_file)
        logger.info(f"处理文件: {file_name}")

        # 获取视频信息
        video_info = get_video_info(video_file)
        if not video_info:
            logger.error(f"无法获取视频信息: {file_name}")
            error_count += 1
            continue

        width = video_info['width']
        height = video_info['height']
        logger.info(f"视频分辨率: {width}x{height}, 比例: {width/height:.4f}")

        # 检查比例
        if is_valid_aspect_ratio(width, height):
            logger.info(f"视频 '{file_name}' 已符合9:16或16:9比例，跳过处理")
            skipped_count += 1
            continue

        # 构建输出路径
        output_file = os.path.join(output_dir, file_name)

        # 处理视频
        if process_video(video_file, output_file, width, height):
            processed_count += 1
        else:
            error_count += 1

    # 打印摘要
    logger.info("\n处理完成!")
    logger.info(f"成功处理: {processed_count} 个文件")
    logger.info(f"已跳过: {skipped_count} 个文件 (符合比例要求)")
    logger.info(f"处理失败: {error_count} 个文件")
    logger.info(f"输出目录: {output_dir}")


if __name__ == "__main__":
    try:
        main()
    except KeyboardInterrupt:
        logger.info("程序被用户中断")
        sys.exit(1)
    except Exception as e:
        logger.exception(f"发生未处理的异常: {str(e)}")
        sys.exit(1)
