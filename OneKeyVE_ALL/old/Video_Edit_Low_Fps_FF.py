'''
使用python实现以下功能需求：
批量降低视频帧率 - 纯FFmpeg实现

功能:
1. 查找当前目录下所有MP4文件
2. 为每个视频创建低帧率版本(3fps)
3. 不保留音频轨道
4. 使用H.264编码和CRF 23保持良好质量
5. 输出文件放在原视频文件所在目录中的output目录内（如果没有则创建），输出文件名和源文件名相同。
6. 跳过已存在的文件
7. 显示详细处理信息
8. 增强错误处理和用户反馈
9.视频的异常处理能力和鲁棒性都要考虑到
99.以上流程处理完之后，检查格式错误，检查语法错误，检查拼写错误，检查调用错误，修改完成后给我完整的代码。

'''


import os
import subprocess
import sys
import logging
import signal
import time
import platform
from pathlib import Path
from typing import List, Optional, Tuple

# 配置日志
logging.basicConfig(
    level=logging.INFO,
    format='%(asctime)s - %(levelname)s - %(message)s',
    handlers=[
        logging.StreamHandler(sys.stdout)
    ]
)
logger = logging.getLogger(__name__)

# 全局变量，用于信号处理
interrupted = False


def signal_handler(sig, frame):
    """处理中断信号"""
    global interrupted
    logger.warning("收到中断信号，正在安全退出...")
    interrupted = True


def setup_signal_handlers():
    """设置适当的信号处理程序"""
    signal.signal(signal.SIGINT, signal_handler)  # Ctrl+C
    if platform.system() != "Windows":
        signal.signal(signal.SIGTERM, signal_handler)  # kill命令


def check_ffmpeg() -> bool:
    """检查FFmpeg是否可用"""
    try:
        # 根据平台调整命令
        ffmpeg_cmd = 'ffmpeg.exe' if platform.system() == 'Windows' else 'ffmpeg'

        result = subprocess.run([ffmpeg_cmd, '-version'],
                                stdout=subprocess.PIPE,
                                stderr=subprocess.PIPE,
                                text=True,
                                check=False)
        if result.returncode == 0:
            version_line = result.stdout.split('\n')[0].strip()
            logger.info(f"FFmpeg已找到: {version_line}")
            return True
        else:
            logger.error("FFmpeg命令执行失败")
            if result.stderr:
                logger.error(f"错误信息: {result.stderr.strip()}")
            return False
    except FileNotFoundError:
        logger.error("FFmpeg未找到，请确保FFmpeg已安装并添加到系统PATH中")
        return False
    except Exception as e:
        logger.error(f"检查FFmpeg时发生错误: {str(e)}")
        return False


def check_ffprobe() -> bool:
    """检查ffprobe是否可用"""
    try:
        ffprobe_cmd = 'ffprobe.exe' if platform.system() == 'Windows' else 'ffprobe'

        result = subprocess.run([ffprobe_cmd, '-version'],
                                stdout=subprocess.PIPE,
                                stderr=subprocess.PIPE,
                                text=True,
                                check=False)
        return result.returncode == 0
    except FileNotFoundError:
        return False
    except Exception:
        return False


def get_video_files(directory: str = '.') -> List[str]:
    """获取目录下所有MP4文件"""
    try:
        # 使用pathlib更可靠地处理路径
        dir_path = Path(directory).resolve()
        files = list(dir_path.glob("*.mp4"))

        # 同时查找小写扩展名的文件 (有些系统区分大小写)
        files += list(dir_path.glob("*.MP4"))

        # 去重
        files = list(set(files))

        logger.info(f"在目录 {dir_path} 中找到 {len(files)} 个MP4文件")
        return [str(file) for file in files]
    except Exception as e:
        logger.error(f"获取视频文件时出错: {str(e)}")
        return []


def create_output_directory(video_path: str) -> Optional[str]:
    """为视频创建输出目录"""
    try:
        video_dir = Path(video_path).parent
        output_dir = video_dir / "output"

        if not output_dir.exists():
            output_dir.mkdir(parents=True, exist_ok=True)
            logger.info(f"创建输出目录: {output_dir}")

        return str(output_dir)
    except Exception as e:
        logger.error(f"创建输出目录时出错: {str(e)}")
        return None


def get_video_info(video_path: str) -> dict:
    """获取视频信息，包括时长和可能的其他元数据"""
    info = {"duration": None, "size": None}

    # 获取文件大小
    try:
        info["size"] = Path(video_path).stat().st_size / (1024 * 1024)  # MB
    except Exception:
        pass

    if not check_ffprobe():
        return info

    try:
        ffprobe_cmd = 'ffprobe.exe' if platform.system() == 'Windows' else 'ffprobe'

        cmd = [
            ffprobe_cmd,
            '-v', 'error',
            '-show_entries', 'format=duration,size',
            '-of', 'json',
            video_path
        ]

        result = subprocess.run(
            cmd,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            check=False
        )

        if result.returncode == 0:
            import json
            try:
                data = json.loads(result.stdout)
                if 'format' in data and 'duration' in data['format']:
                    duration_str = data['format']['duration']
                    if duration_str.replace('.', '', 1).isdigit():
                        info["duration"] = float(duration_str)
            except json.JSONDecodeError:
                pass
    except Exception as e:
        logger.debug(f"获取视频信息时出错: {str(e)}")

    return info


def run_ffmpeg_with_progress(cmd: list, video_name: str) -> Tuple[bool, int, float]:
    """运行FFmpeg命令并显示进度"""
    global interrupted

    try:
        start_time = time.time()
        frame_count = 0

        # 为Windows调整命令
        if platform.system() == 'Windows':
            cmd[0] = 'ffmpeg.exe' if not cmd[0].endswith('.exe') else cmd[0]

        logger.debug(f"执行命令: {' '.join(cmd)}")

        # 使用Popen执行命令
        process = subprocess.Popen(
            cmd,
            stdout=subprocess.PIPE,
            stderr=subprocess.STDOUT,
            text=True,
            universal_newlines=True,
            bufsize=1,
            encoding='utf-8',
            errors='replace'
        )

        # 处理输出
        last_update = 0
        line_buffer = ""

        while True:
            if interrupted:
                logger.warning("用户中断处理，正在终止FFmpeg进程...")
                process.terminate()
                try:
                    process.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    process.kill()
                return False, frame_count, time.time() - start_time

            if process.stdout is None:
                break

            line = process.stdout.readline()
            if not line and process.poll() is not None:
                break

            if line:
                line_buffer += line
                if "\n" in line_buffer:
                    full_line = line_buffer.strip()
                    line_buffer = ""

                    # 记录调试信息
                    if any(kw in full_line for kw in ["frame=", "fps=", "time=", "bitrate=", "speed="]):
                        current_time = time.time()
                        # 限制更新频率，避免太多日志
                        if current_time - last_update > 1 or "speed=" in full_line:
                            logger.debug(f"[{video_name}] {full_line}")
                            last_update = current_time

                        # 尝试提取帧数
                        try:
                            if "frame=" in full_line:
                                parts = full_line.split()
                                for part in parts:
                                    if part.startswith("frame="):
                                        frame_str = part.split("=")[1].strip()
                                        if frame_str.isdigit():
                                            frame_count = int(frame_str)
                                        break
                        except Exception:
                            pass

        return_code = process.wait()
        processing_time = time.time() - start_time

        return return_code == 0, frame_count, processing_time

    except Exception as e:
        logger.error(f"执行FFmpeg时出错: {str(e)}")
        return False, 0, 0


def process_video(video_path: str) -> bool:
    """处理单个视频文件"""
    global interrupted

    if interrupted:
        logger.warning("处理被用户中断")
        return False

    try:
        # 获取文件名和目录
        video_name = Path(video_path).name
        logger.info(f"开始处理: {video_name}")

        # 获取视频信息
        video_info = get_video_info(video_path)
        duration_info = f" (时长: {video_info['duration']:.2f}秒)" if video_info['duration'] else ""
        size_info = f" (大小: {video_info['size']:.2f}MB)" if video_info['size'] else ""
        logger.info(f"源文件信息:{duration_info}{size_info}")

        # 创建输出目录
        output_dir = create_output_directory(video_path)
        if not output_dir:
            return False

        # 构建输出文件路径
        output_path = os.path.join(output_dir, video_name)

        # 检查输出文件是否已存在
        if os.path.exists(output_path):
            logger.info(f"跳过已存在的文件: {video_name}")
            return True

        logger.info(f"输出文件: {output_path}")

        # 构建FFmpeg命令
        cmd = [
            'ffmpeg',
            '-i', video_path,
            '-an',                        # 不包含音频
            '-c:v', 'libx264',            # 使用H.264编码
            '-crf', '23',                 # 设置CRF值为23
            '-pix_fmt', 'yuv420p',        # 确保兼容性
            '-vf', 'fps=3',               # 设置帧率为3fps
            '-y',                         # 覆盖输出文件
            output_path
        ]

        # 运行FFmpeg
        success, frame_count, processing_time = run_ffmpeg_with_progress(
            cmd, video_name)

        # 检查执行结果
        if success:
            logger.info(f"成功处理视频: {video_name}")
            logger.info(f"处理时间: {processing_time:.2f} 秒")
            logger.info(f"处理帧数: {frame_count}")

            # 获取输出文件信息
            if os.path.exists(output_path):
                try:
                    output_size = Path(output_path).stat(
                    ).st_size / (1024 * 1024)  # MB
                    logger.info(f"输出文件大小: {output_size:.2f} MB")

                    if video_info['size'] and video_info['size'] > 0:
                        compression_ratio = output_size / \
                            video_info['size'] * 100
                        logger.info(f"压缩率: {compression_ratio:.2f}%")
                except Exception as e:
                    logger.debug(f"获取输出文件信息时出错: {str(e)}")

            return True
        else:
            logger.error(f"处理视频失败: {video_name}")
            # 清理不完整的文件
            if os.path.exists(output_path):
                try:
                    file_size = Path(output_path).stat().st_size
                    if file_size < 1024 * 100:  # 小于100KB认为是不完整文件
                        os.remove(output_path)
                        logger.info(f"已删除不完整的输出文件: {output_path}")
                except Exception as e:
                    logger.warning(f"清理不完整文件时出错: {str(e)}")
            return False

    except Exception as e:
        logger.error(f"处理视频 {video_path} 时出错: {str(e)}")
        return False


def main():
    """主函数"""
    global interrupted

    logger.info("=== 视频帧率降低工具 ===")
    logger.info(
        f"运行环境: Python {sys.version.split()[0]}, {platform.system()} {platform.release()}")

    # 设置信号处理
    setup_signal_handlers()

    # 检查FFmpeg
    if not check_ffmpeg():
        logger.error("请安装FFmpeg并将其添加到系统PATH中，然后重试。")
        logger.error("安装指南: https://ffmpeg.org/download.html")
        sys.exit(1)

    # 检查ffprobe
    ffprobe_available = check_ffprobe()
    if not ffprobe_available:
        logger.warning("ffprobe未找到，将无法显示详细的视频信息")

    # 获取当前目录下所有MP4文件
    current_dir = os.getcwd()
    video_files = get_video_files(current_dir)

    if not video_files:
        logger.warning("未找到MP4文件")
        logger.info(f"当前目录: {current_dir}")
        logger.info("请确保在包含MP4文件的目录中运行此脚本")
        sys.exit(0)

    # 显示要处理的文件列表
    logger.info(f"\n找到 {len(video_files)} 个MP4文件:")
    for i, file in enumerate(video_files, 1):
        file_path = Path(file)
        try:
            file_size = file_path.stat().st_size / (1024 * 1024)  # MB
            logger.info(f"{i}. {file_path.name} ({file_size:.2f} MB)")
        except Exception:
            logger.info(f"{i}. {file_path.name}")

    logger.info(f"\n开始处理 {len(video_files)} 个文件，按 Ctrl+C 可随时中断处理\n")

    # 处理每个视频文件
    success_count = 0
    failure_count = 0

    for i, video_file in enumerate(video_files, 1):
        if interrupted:
            logger.warning("用户中断了处理过程")
            break

        logger.info(f"\n{'='*50}")
        logger.info(f"处理文件 {i}/{len(video_files)}: {Path(video_file).name}")
        logger.info(f"{'='*50}")

        if process_video(video_file):
            success_count += 1
        else:
            failure_count += 1

    # 显示总结
    logger.info(f"\n{'='*50}")
    logger.info("处理完成总结")
    logger.info(f"{'='*50}")
    logger.info(f"成功处理: {success_count} 个文件")
    logger.info(f"处理失败: {failure_count} 个文件")
    logger.info(f"跳过: {len(video_files) - success_count - failure_count} 个文件")

    if failure_count > 0:
        logger.error("部分文件处理失败，请检查日志获取详细信息")
        sys.exit(1)
    elif interrupted:
        logger.warning("处理过程被用户中断")
        sys.exit(1)
    else:
        logger.info("🎉 所有文件处理成功！")
        sys.exit(0)


if __name__ == "__main__":
    main()
