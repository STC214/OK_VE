"""

用python程序完成需求，要求使用纯FFmpeg实现，能用cuda加速处理的部分用cuda处理。程序的异常处理能力和鲁棒性都要考虑到：
0. 视频文件可能是1个或者多个，所以是处理批量视频文件。
当前程序的文件名是Video_Edit_FF.py（含后缀），我希望直接运行：python Video_Edit_FF.py 
即可自动完成所有功能，而不是需要我额外的添加视频文件的文件名才可以。

1. 判断视频文件比例是否为9:16或者16:9，如果不是则进行2。如果是则跳过此视频文件的处理。

2. 我们将视频比例如9:16看作是一个数学上的分数，如9:16就是分数9/16， 
   如果视频比例大于9/16则进行3，如果视频比例小于9/16则进行4。

3. 新建一个临时视频，画幅设定为9：16，画幅分辨率中的宽度同原视频的宽度。然后进行5。

4. 新建一个临时视频，画幅设定为9：16，画幅分辨率中的高度同原视频的高度。然后进行5。

5. 将原视频拷贝到临时视频中，拷贝过来的原视频的中心点应当和画幅的中心点重合，此时形成轨道01， 
   轨道01以中心点不变等比例放大100%。裁切掉超出画幅的部分以节省文件体积。 
   对轨道01进行高斯模糊，模糊参数就用常用值即可。 
   
   再次拷贝原视频到临时视频中，同样拷贝过来的原视频的中心点应当和画幅的中心点重合，形成轨道02.
   临时视频的轨道02导入原视频之后，正确的状态是原视频的中心点和临时视频的画幅的中心点重合。 
   轨道02无需进行任何缩放操作。 
   
   由于轨道02上的视频在此处理逻辑中必然会导致填不满宽度或者高度其中一个维度， 
   这些填不满的部分会默认用黑色填充，将这些用黑色填充的部分裁切掉， 
   这样轨道02裁切掉的部分就可以正常显示出轨道01的内容。对于轨道02未被裁切的部分进行边缘羽化，羽化值为5像素。
   

6. 保证所有轨道可见。 
   合成视频到当前目录下的output目录（若没有则新建）， 导出的视频文件名为原视频文件名。

7.对于可以使用cuda加快处理的部分要使用cuda。显存使用允许最高使用4GB。

99.以上流程处理完之后，检查格式错误，检查语法错误，检查拼写错误，检查调用错误，修改完成后给我完整的代码。
999.加上详细注释

"""

import os
import subprocess
import sys
import json
import glob
import shlex
import logging
import time
import platform
import signal
import io
import math
from pathlib import Path
from typing import Optional, Tuple, List, Dict, Any

# 配置日志 - 避免使用特殊Unicode字符，确保Windows兼容性


def setup_logging():
    """设置兼容Windows的日志系统"""
    # 创建日志目录
    log_dir = "logs"
    os.makedirs(log_dir, exist_ok=True)

    # 带时间戳的日志文件名
    timestamp = time.strftime("%Y%m%d_%H%M%S")
    log_file = os.path.join(log_dir, f"video_edit_{timestamp}.log")

    # 配置日志格式
    formatter = logging.Formatter('%(asctime)s - %(levelname)s - %(message)s')

    # 文件处理器
    file_handler = logging.FileHandler(log_file, encoding='utf-8')
    file_handler.setFormatter(formatter)

    # 控制台处理器 - 确保Windows兼容
    console_handler = logging.StreamHandler()
    if platform.system() == "Windows":
        # 在Windows上使用兼容编码
        console_handler = logging.StreamHandler(
            stream=io.TextIOWrapper(
                sys.stdout.buffer, encoding='utf-8', errors='replace')
        )
    console_handler.setFormatter(formatter)

    # 配置根日志记录器
    logger = logging.getLogger()
    logger.setLevel(logging.INFO)
    logger.addHandler(file_handler)
    logger.addHandler(console_handler)

    return logger


# 初始化日志系统
logger = setup_logging()

# 全局变量
use_cuda = False  # CUDA加速标志
interrupted = False  # 中断标志
hardware_acceleration = ""  # 硬件加速方式
ffmpeg_version = ""  # FFmpeg版本信息


def signal_handler(sig, frame):
    """处理中断信号"""
    global interrupted
    logger.warning("收到中断信号，正在安全退出...")
    interrupted = True


def setup_signal_handlers():
    """设置信号处理程序"""
    signal.signal(signal.SIGINT, signal_handler)  # Ctrl+C
    if platform.system() != "Windows":
        signal.signal(signal.SIGTERM, signal_handler)  # kill命令


def get_available_cuda_devices():
    """获取可用的CUDA设备列表"""
    try:
        if platform.system() == "Windows":
            nvidia_smi_cmd = "nvidia-smi.exe"
        else:
            nvidia_smi_cmd = "nvidia-smi"

        result = subprocess.run(
            [nvidia_smi_cmd, "--query-gpu=index,name,memory.total", "--format=csv"],
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            check=True
        )

        lines = result.stdout.strip().split('\n')[1:]  # 跳过标题行
        devices = []
        for line in lines:
            if line.strip():
                parts = line.split(',')
                if len(parts) >= 3:
                    device_id = parts[0].strip()
                    device_name = parts[1].strip()
                    memory = parts[2].strip()
                    devices.append((device_id, device_name, memory))

        if devices:
            logger.info(f"检测到 {len(devices)} 个 NVIDIA GPU 设备:")
            for device_id, device_name, memory in devices:
                logger.info(f"GPU {device_id}: {device_name}, 显存: {memory}")

        return devices
    except Exception as e:
        logger.debug(f"获取CUDA设备信息失败: {str(e)}")
        return []


def check_ffmpeg():
    """检查ffmpeg和ffprobe是否可用"""
    global ffmpeg_version

    try:
        # 根据平台调整命令
        ffmpeg_cmd = 'ffmpeg.exe' if platform.system() == 'Windows' else 'ffmpeg'
        ffprobe_cmd = 'ffprobe.exe' if platform.system() == 'Windows' else 'ffprobe'

        # 检查ffmpeg
        ffmpeg_result = subprocess.run(
            [ffmpeg_cmd, '-version'],
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            check=True
        )

        # 检查ffprobe
        ffprobe_result = subprocess.run(
            [ffprobe_cmd, '-version'],
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            check=True
        )

        # 提取FFmpeg版本信息
        ffmpeg_version_line = ffmpeg_result.stdout.split('\n')[0]
        ffmpeg_version = ffmpeg_version_line.split('Copyright')[0].strip()
        logger.info(f"FFmpeg版本: {ffmpeg_version}")

        return True
    except (subprocess.CalledProcessError, FileNotFoundError) as e:
        logger.error(f"FFmpeg或FFprobe检查失败: {str(e)}")
        if hasattr(e, 'stderr') and e.stderr:
            logger.error(f"错误详情: {e.stderr.strip()}")
        logger.error("请确保FFmpeg已安装并添加到系统PATH中")
        return False


def check_ffmpeg_filters():
    """检查FFmpeg支持的滤镜"""
    global use_cuda

    try:
        ffmpeg_cmd = 'ffmpeg.exe' if platform.system() == 'Windows' else 'ffmpeg'

        # 获取FFmpeg支持的滤镜
        filters_cmd = [
            ffmpeg_cmd,
            '-filters'
        ]

        filters_result = subprocess.run(
            filters_cmd,
            stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
            text=True,
            check=False
        )

        if filters_result.returncode != 0:
            logger.warning(f"获取FFmpeg滤镜列表失败，返回码: {filters_result.returncode}")
            return False

        filters_text = filters_result.stdout.lower()

        # 检查必需的CPU滤镜
        required_cpu_filters = ['scale', 'crop', 'gblur', 'pad', 'overlay']
        missing_cpu_filters = [
            f for f in required_cpu_filters if f not in filters_text]

        if missing_cpu_filters:
            logger.error(f"FFmpeg缺少必需的CPU滤镜: {', '.join(missing_cpu_filters)}")
            return False

        # 检查CUDA滤镜
        cuda_filters_available = False
        cuda_filters = {
            'crop_cuda': 'crop_cuda' in filters_text,
            'gblur_cuda': 'gblur_cuda' in filters_text,
            'scale_cuda': 'scale_cuda' in filters_text,
            'overlay_cuda': 'overlay_cuda' in filters_text,
            'hwdownload': 'hwdownload' in filters_text,
            'hwupload_cuda': 'hwupload_cuda' in filters_text
        }

        missing_cuda_filters = [
            f for f, available in cuda_filters.items() if not available]

        if missing_cuda_filters:
            logger.warning(
                f"FFmpeg缺少必需的CUDA滤镜: {', '.join(missing_cuda_filters)}")
        else:
            cuda_filters_available = True
            logger.info("所有必需的CUDA滤镜都可用")

        return cuda_filters_available

    except Exception as e:
        logger.error(f"检查FFmpeg滤镜时出错: {str(e)}", exc_info=True)
        return False


def check_ffmpeg_hwaccels():
    """检查FFmpeg支持的硬件加速方式"""
    global hardware_acceleration

    try:
        ffmpeg_cmd = 'ffmpeg.exe' if platform.system() == 'Windows' else 'ffmpeg'

        # 获取FFmpeg支持的硬件加速方式
        hwaccel_cmd = [
            ffmpeg_cmd,
            '-hide_banner',
            '-hwaccels'
        ]

        hwaccel_result = subprocess.run(
            hwaccel_cmd,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            check=False
        )

        if hwaccel_result.returncode != 0:
            logger.warning(f"获取硬件加速信息失败，返回码: {hwaccel_result.returncode}")
            if hwaccel_result.stderr:
                logger.warning(f"错误信息: {hwaccel_result.stderr.strip()}")
            return []

        logger.info("FFmpeg支持的硬件加速方式:")
        hwaccels = []
        for line in hwaccel_result.stdout.splitlines():
            line = line.strip()
            if line and line != 'Hardware acceleration methods:':
                hwaccels.append(line)
                logger.info(f"  {line}")

        # 优先选择CUDA
        if 'cuda' in hwaccels:
            hardware_acceleration = 'cuda'
            logger.info("选择CUDA作为硬件加速方式")
        elif hwaccels:
            hardware_acceleration = hwaccels[0]
            logger.info(f"选择{hardware_acceleration}作为硬件加速方式")

        return hwaccels

    except Exception as e:
        logger.error(f"检查硬件加速支持时出错: {str(e)}", exc_info=True)
        return []


def check_cuda_support():
    """综合检查CUDA支持和NVIDIA GPU可用性"""
    global use_cuda, hardware_acceleration

    logger.info("检查CUDA支持...")

    try:
        # 1. 检查NVIDIA设备
        devices = get_available_cuda_devices()
        if not devices:
            logger.info("未检测到可用的NVIDIA GPU设备")
            return False

        # 2. 检查FFmpeg硬件加速支持
        hwaccels = check_ffmpeg_hwaccels()
        if not hwaccels:
            logger.info("FFmpeg未报告任何硬件加速支持")
            return False

        if 'cuda' not in hwaccels:
            logger.info("FFmpeg不支持CUDA硬件加速")
            return False

        # 3. 检查FFmpeg滤镜支持
        cuda_filters_available = check_ffmpeg_filters()

        # 4. 检查CUDA编码器
        ffmpeg_cmd = 'ffmpeg.exe' if platform.system() == 'Windows' else 'ffmpeg'
        encoders_cmd = [ffmpeg_cmd, '-encoders']
        encoders_result = subprocess.run(
            encoders_cmd,
            stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
            text=True,
            check=False
        )

        cuda_encoders_available = False
        if encoders_result.returncode == 0:
            encoders_text = encoders_result.stdout.lower()
            if 'h264_nvenc' in encoders_text:
                cuda_encoders_available = True
                logger.info("检测到FFmpeg支持CUDA编码 (h264_nvenc)")
            else:
                logger.warning("FFmpeg不支持CUDA编码器(h264_nvenc)")

        # 综合判断
        use_cuda = cuda_filters_available and cuda_encoders_available

        if use_cuda:
            logger.info("CUDA加速已启用")
        else:
            reasons = []
            if not cuda_filters_available:
                reasons.append("缺少必需的CUDA滤镜")
            if not cuda_encoders_available:
                reasons.append("缺少CUDA编码器支持")
            logger.warning(f"CUDA加速不可用: {', '.join(reasons)}")
            logger.info("将使用CPU模式处理视频")

        return use_cuda

    except Exception as e:
        logger.error(f"检查CUDA支持时出错: {str(e)}", exc_info=True)
        return False


def get_video_info(file_path: str) -> Optional[dict]:
    """获取视频信息，包括分辨率和时长"""
    try:
        ffprobe_cmd = 'ffprobe.exe' if platform.system() == 'Windows' else 'ffprobe'
        cmd = [
            ffprobe_cmd,
            '-v', 'error',
            '-select_streams', 'v:0',
            '-show_entries', 'stream=width,height,duration,codec_name,bit_rate,r_frame_rate',
            '-of', 'json',
            file_path
        ]
        result = subprocess.run(
            cmd,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            text=True,
            check=True
        )
        info = json.loads(result.stdout)
        if not info.get('streams'):
            logger.error(f"视频文件不包含有效的视频流: {file_path}")
            return None

        stream_info = info['streams'][0]
        # 计算帧率
        r_frame_rate = stream_info.get('r_frame_rate', '30/1')
        try:
            num, den = map(int, r_frame_rate.split('/'))
            fps = num / den
        except:
            fps = 30.0

        return {
            'width': int(stream_info['width']),
            'height': int(stream_info['height']),
            'duration': float(stream_info.get('duration', 0)),
            'codec': stream_info.get('codec_name', 'unknown'),
            'bit_rate': int(stream_info.get('bit_rate', 0)) if stream_info.get('bit_rate') else 0,
            'fps': fps
        }
    except (subprocess.CalledProcessError, json.JSONDecodeError, KeyError, ValueError) as e:
        logger.error(f"获取视频信息失败 '{file_path}': {str(e)}")
        if hasattr(e, 'stderr') and e.stderr:
            logger.error(f"FFprobe错误: {e.stderr.strip()}")
        return None
    except Exception as e:
        logger.error(f"获取视频信息时发生意外错误 '{file_path}': {str(e)}", exc_info=True)
        return None


def is_valid_aspect_ratio(width: int, height: int) -> bool:
    """检查是否为9:16或16:9比例"""
    if height == 0:
        logger.warning("视频高度为0，无法计算比例")
        return False

    ratio = width / height
    target_ratio_9_16 = 9 / 16  # 0.5625
    target_ratio_16_9 = 16 / 9  # 1.777...

    # 允许一些浮动误差
    tolerance = 0.01

    is_9_16 = abs(ratio - target_ratio_9_16) < tolerance
    is_16_9 = abs(ratio - target_ratio_16_9) < tolerance

    if is_9_16:
        logger.debug(
            f"检测到9:16比例: {width}x{height} = {ratio:.4f} (目标: {target_ratio_9_16:.4f})")
    elif is_16_9:
        logger.debug(
            f"检测到16:9比例: {width}x{height} = {ratio:.4f} (目标: {target_ratio_16_9:.4f})")

    return is_9_16 or is_16_9


def format_time(seconds: float) -> str:
    """格式化时间显示"""
    if seconds < 0:
        seconds = 0

    hours = int(seconds // 3600)
    minutes = int((seconds % 3600) // 60)
    seconds = seconds % 60

    if hours > 0:
        return f"{hours}h {minutes}m {seconds:.1f}s"
    elif minutes > 0:
        return f"{minutes}m {seconds:.1f}s"
    else:
        return f"{seconds:.1f}s"


def get_file_size_mb(file_path: str) -> float:
    """获取文件大小(MB)"""
    try:
        return os.path.getsize(file_path) / (1024 * 1024)
    except Exception as e:
        logger.debug(f"获取文件大小失败: {str(e)}")
        return 0.0


def run_ffmpeg_with_progress(cmd: list, input_path: str, output_path: str, duration: float = 0) -> Tuple[bool, float]:
    """运行FFmpeg命令并显示进度"""
    global interrupted

    try:
        start_time = time.time()
        video_name = Path(input_path).name
        logger.info(f"开始处理: {video_name}")
        logger.debug(f"执行命令: {' '.join(str(arg) for arg in cmd)}")

        env = os.environ.copy()
        # 设置CUDA环境变量以获得更好的兼容性
        env["CUDA_LAUNCH_BLOCKING"] = "1"
        env["TF_FORCE_GPU_ALLOW_GROWTH"] = "true"

        # 重定向所有输出到pipe，以便处理
        process = subprocess.Popen(
            cmd,
            stdout=subprocess.PIPE,
            stderr=subprocess.PIPE,
            universal_newlines=True,
            bufsize=1,
            env=env,
            encoding='utf-8',
            errors='replace'
        )

        # 使用单独的线程读取输出，避免阻塞
        import threading
        stderr_lines = []
        stdout_lines = []

        stderr_thread = threading.Thread(target=lambda: [stderr_lines.append(
            line) for line in iter(process.stderr.readline, '')])
        stdout_thread = threading.Thread(target=lambda: [stdout_lines.append(
            line) for line in iter(process.stdout.readline, '')])

        stderr_thread.daemon = True
        stdout_thread.daemon = True
        stderr_thread.start()
        stdout_thread.start()

        # 读取stderr以显示进度
        last_update = 0
        last_progress = 0
        reported_progress = False

        while process.poll() is None:
            if interrupted:
                logger.warning("用户中断处理，正在终止FFmpeg进程...")
                process.terminate()
                try:
                    process.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    logger.warning("进程终止超时，强制杀死进程")
                    process.kill()
                return False, time.time() - start_time

            # 处理stderr中的进度信息
            current_stderr = stderr_lines.copy()
            stderr_lines.clear()

            for line in current_stderr:
                line = line.strip()
                if not line:
                    continue

                # 检查时间进度
                if "time=" in line:
                    # 提取时间信息
                    time_str = None
                    for part in line.split():
                        if part.startswith("time="):
                            time_str = part.split("=")[1]
                            break

                    if time_str and duration > 0:
                        try:
                            # 解析时间格式 (HH:MM:SS.mmm 或 HH:MM:SS)
                            time_parts = time_str.split(':')
                            if len(time_parts) == 3:
                                hours = int(float(time_parts[0]))
                                minutes = int(float(time_parts[1]))
                                seconds = float(time_parts[2])
                                elapsed_seconds = hours * 3600 + minutes * 60 + seconds

                                # 计算进度
                                progress = min(
                                    elapsed_seconds / duration * 100, 100)

                                # 限制更新频率，只在进度变化大于1%或超过2秒时更新
                                current_time = time.time()
                                if (progress - last_progress > 1.0 or current_time - last_update > 2) and progress > 0:
                                    logger.info(
                                        f"处理中: {video_name} - "
                                        f"进度: {progress:.1f}% - "
                                        f"已用时间: {format_time(elapsed_seconds)}"
                                    )
                                    last_progress = progress
                                    last_update = current_time
                                    reported_progress = True
                        except (ValueError, IndexError) as e:
                            logger.debug(f"解析进度信息时出错: {str(e)}")

            # 避免CPU使用过高
            time.sleep(0.1)

        # 等待线程完成
        stderr_thread.join(timeout=2)
        stdout_thread.join(timeout=2)

        # 检查返回码
        return_code = process.returncode
        if return_code != 0:
            # 收集所有错误信息
            stderr_content = "".join(stderr_lines)
            stdout_content = "".join(stdout_lines)

            # 尝试获取更多错误信息
            if not stderr_content and hasattr(process, 'stderr') and not process.stderr.closed:
                try:
                    stderr_content = process.stderr.read()
                except:
                    pass

            # 特殊处理Windows返回码
            if platform.system() == "Windows" and return_code < 0:
                # Windows返回码是补码形式
                actual_return_code = return_code & 0xFFFFFFFF
                if actual_return_code != return_code:
                    logger.error(
                        f"FFmpeg处理失败 '{input_path}': 返回码 {return_code} (Windows: {actual_return_code})")
                else:
                    logger.error(
                        f"FFmpeg处理失败 '{input_path}': 返回码 {return_code}")
            else:
                logger.error(f"FFmpeg处理失败 '{input_path}': 返回码 {return_code}")

            # 详细错误分析
            if stderr_content:
                logger.error(f"错误详情:\n{stderr_content.strip()}")
                # 特殊错误模式检测
                if "Input frame sizes do not match" in stderr_content:
                    logger.error("错误: 检测到帧尺寸不匹配。这通常发生在alphamerge滤镜中。")
                    logger.error("建议: 简化滤镜链，避免使用复杂的遮罩操作。")
                elif "Failed to configure output pad" in stderr_content:
                    logger.error("错误: 滤镜链配置失败。这通常发生在滤镜参数不兼容时。")
                elif "CUDA" in stderr_content or "cuvid" in stderr_content.lower():
                    logger.error("检测到CUDA相关错误，可能是GPU内存不足或驱动不兼容")
                    logger.error("建议尝试：")
                    logger.error("1. 关闭其他占用GPU的应用程序")
                    logger.error("2. 减小视频分辨率或使用CPU模式处理")
                    logger.error("3. 更新NVIDIA驱动和CUDA工具包")
            if stdout_content:
                logger.debug(f"标准输出:\n{stdout_content.strip()}")

            # 记录详细的错误信息到文件
            error_log_file = os.path.join(
                "logs", f"ffmpeg_error_{Path(input_path).stem}_{int(time.time())}.log")
            try:
                with open(error_log_file, 'w', encoding='utf-8') as f:
                    f.write(f"Command: {' '.join(str(arg) for arg in cmd)}\n")
                    f.write(f"Return code: {return_code}\n")
                    f.write(f"Stderr:\n{stderr_content}\n")
                    f.write(f"Stdout:\n{stdout_content}\n")
                logger.info(f"详细错误信息已保存到: {error_log_file}")
            except Exception as e:
                logger.debug(f"保存错误日志失败: {str(e)}")

            return False, time.time() - start_time

        # 即使成功，如果没有报告任何进度，也记录一下
        if not reported_progress and duration > 0:
            logger.info(f"处理完成: {video_name} - 处理速度较快，未报告进度")

        processing_time = time.time() - start_time
        logger.info(
            f"处理完成: '{video_name}' - 耗时: {format_time(processing_time)}")
        return True, processing_time

    except Exception as e:
        logger.error(f"执行FFmpeg命令时出错: {str(e)}", exc_info=True)
        if 'process' in locals() and process.poll() is None:
            try:
                process.terminate()
                process.wait(timeout=5)
            except Exception as te:
                logger.warning(f"终止进程时出错: {str(te)}")
                try:
                    process.kill()
                except:
                    pass
        return False, time.time() - start_time


def build_filter_complex(input_width, input_height, target_width, target_height, use_cuda_filters=False):
    """构建滤镜链，避免帧尺寸不匹配问题"""

    if use_cuda_filters:
        # CUDA版本滤镜链 - 简化设计，避免alphamerge
        filter_parts = [
            # 背景层: 放大视频以填充整个9:16画布
            f"[0:v]scale_cuda={target_width}:{target_height}:force_original_aspect_ratio=increase,"
            f"crop_cuda={target_width}:{target_height}[bg_raw]",
            # 背景模糊
            "[bg_raw]gblur_cuda=sigma=8[bg]",
            # 前景层: 缩小视频以适应9:16画布，保持比例
            f"[0:v]scale_cuda={target_width}:{target_height}:force_original_aspect_ratio=decrease[fg]",
            # 将前景叠放在背景中央
            "[bg][fg]overlay_cuda=(main_w-overlay_w)/2:(main_h-overlay_h)/2[out]"
        ]
    else:
        # CPU版本滤镜链 - 简化设计，避免alphamerge
        filter_parts = [
            # 背景层: 放大视频以填充整个9:16画布
            f"[0:v]scale={target_width}:{target_height}:force_original_aspect_ratio=increase,"
            f"crop={target_width}:{target_height}[bg_raw]",
            # 背景模糊
            "[bg_raw]gblur=sigma=10[bg]",
            # 前景层: 缩小视频以适应9:16画布，保持比例
            f"[0:v]scale={target_width}:{target_height}:force_original_aspect_ratio=decrease[fg]",
            # 将前景叠放在背景中央
            "[bg][fg]overlay=(main_w-overlay_w)/2:(main_h-overlay_h)/2[out]"
        ]

    filter_complex = ";".join(filter_parts)
    return filter_complex


def process_video(input_path: str, output_path: str, width: int, height: int, duration: float, use_cuda_fallback: bool = False) -> bool:
    """处理不符合比例的视频"""
    global use_cuda, hardware_acceleration, interrupted

    if interrupted:
        logger.warning("处理被用户中断")
        return False

    try:
        video_name = Path(input_path).name
        logger.info(f"\n{'='*60}")
        logger.info(f"处理视频: {video_name}")
        logger.info(f"原始分辨率: {width}x{height}, 比例: {width/height:.4f}")
        input_size_mb = get_file_size_mb(input_path)
        logger.info(f"输入文件大小: {input_size_mb:.2f} MB")

        # 计算比例
        ratio = width / height
        target_ratio = 9 / 16  # 9:16比例

        # 确定新画布尺寸
        if ratio > target_ratio:
            # 宽度为主
            new_width = width
            new_height = int(width / target_ratio)
            logger.info(f"视频比例({ratio:.4f}) > 9:16({target_ratio:.4f}), 以宽度为主")
        else:
            # 高度为主
            new_height = height
            new_width = int(height * target_ratio)
            logger.info(f"视频比例({ratio:.4f}) < 9:16({target_ratio:.4f}), 以高度为主")

        # 确保尺寸为偶数 (FFmpeg要求)
        original_width, original_height = new_width, new_height
        new_width = new_width if new_width % 2 == 0 else new_width + 1
        new_height = new_height if new_height % 2 == 0 else new_height + 1

        if (new_width, new_height) != (original_width, original_height):
            logger.info(
                f"调整分辨率以符合FFmpeg要求: {original_width}x{original_height} -> {new_width}x{new_height}")

        logger.info(f"目标分辨率: {new_width}x{new_height} (9:16)")

        # 决定是否使用CUDA
        cuda_mode = use_cuda and not use_cuda_fallback

        # 对于大文件或高分辨率视频，更谨慎地使用CUDA
        if (input_size_mb > 60 or max(width, height) > 1920) and cuda_mode:
            # 检查GPU显存
            devices = get_available_cuda_devices()
            if devices:
                # 简单的显存检查 - 假设需要至少4GB显存
                memory_str = devices[0][2]
                if "MB" in memory_str:
                    memory_val = float(memory_str.split()[0])
                    if memory_val < 4096:  # 4GB
                        logger.warning(
                            f"GPU显存不足 ({memory_str})，对于大文件建议使用CPU模式")
                        cuda_mode = False

        # 构建FFmpeg命令
        if cuda_mode:
            logger.info("使用CUDA GPU加速处理")

            # 确保目标分辨率适合GPU处理
            max_resolution = 4096  # 4K
            if max(new_width, new_height) > max_resolution:
                scale_factor = max_resolution / max(new_width, new_height)
                new_width = int(
                    new_width * scale_factor) if new_width > new_height else max_resolution
                new_height = int(
                    new_height * scale_factor) if new_height > new_width else max_resolution
                logger.warning(f"分辨率过高，为GPU处理调整为: {new_width}x{new_height}")

            # 构建CUDA命令
            cmd = [
                'ffmpeg',
                '-hwaccel', 'cuda',
                '-hwaccel_output_format', 'cuda',
                '-i', input_path,
                '-filter_complex'
            ]

            # 构建滤镜链
            filter_complex = build_filter_complex(
                width, height, new_width, new_height, use_cuda_filters=True)
            cmd.append(filter_complex)

            # 输出参数
            cmd.extend([
                '-map', '[out]',
                '-map', '0:a?',  # 如果有音频则复制
                '-c:v', 'h264_nvenc',
                '-preset', 'medium',  # 使用中等预设
                '-profile:v', 'main',
                '-rc:v', 'vbr_hq',
                '-b:v', '0',
                '-cq', '23',
                '-c:a', 'aac',
                '-b:a', '128k',
                '-movflags', '+faststart',
                '-y',  # 覆盖输出文件
                output_path
            ])
        else:
            if cuda_mode:
                logger.warning("CUDA模式不可用，回退到CPU处理")

            logger.info("使用CPU软件处理")

            # 构建CPU命令
            cmd = [
                'ffmpeg',
                '-i', input_path,
                '-filter_complex'
            ]

            # 构建滤镜链
            filter_complex = build_filter_complex(
                width, height, new_width, new_height, use_cuda_filters=False)
            cmd.append(filter_complex)

            # 输出参数
            cmd.extend([
                '-map', '[out]',
                '-map', '0:a?',  # 如果有音频则复制
                '-c:v', 'libx264',
                '-preset', 'medium',
                '-crf', '23',
                '-profile:v', 'main',
                '-pix_fmt', 'yuv420p',  # 确保兼容性
                '-movflags', '+faststart',
                '-c:a', 'aac',
                '-b:a', '128k',
                '-y',  # 覆盖输出文件
                output_path
            ])

        # 运行FFmpeg命令
        success, processing_time = run_ffmpeg_with_progress(
            cmd, input_path, output_path, duration)

        if success:
            # 验证输出文件
            if os.path.exists(output_path):
                output_size = os.path.getsize(output_path)
                if output_size > 1024:  # 至少1KB
                    output_size_mb = output_size / (1024 * 1024)
                    compression_ratio = input_size_mb / output_size_mb if output_size_mb > 0 else 0

                    logger.info(
                        f"成功处理: '{video_name}' -> '{Path(output_path).name}'")
                    logger.info(f"输出文件大小: {output_size_mb:.2f} MB")
                    if compression_ratio > 0:
                        logger.info(f"压缩比例: 1:{compression_ratio:.1f}")

                    return True
                else:
                    logger.error(
                        f"输出文件太小，可能处理失败: {output_path} ({output_size} bytes)")
            else:
                logger.error(f"输出文件不存在: {output_path}")

        # 处理失败，尝试回退
        if cuda_mode and not use_cuda_fallback:
            logger.warning("CUDA处理失败，尝试回退到CPU处理...")
            return process_video(input_path, output_path, width, height, duration, use_cuda_fallback=True)

        logger.error(f"处理失败: '{video_name}'")
        return False

    except MemoryError:
        logger.error(f"内存不足错误处理 '{Path(input_path).name}'", exc_info=True)
        if use_cuda and not use_cuda_fallback:
            logger.warning("CUDA内存不足，尝试回退到CPU处理...")
            return process_video(input_path, output_path, width, height, duration, use_cuda_fallback=True)
        return False
    except Exception as e:
        logger.error(
            f"处理视频时出错 '{Path(input_path).name}': {str(e)}", exc_info=True)
        logger.error(f"错误类型: {type(e).__name__}")

        # CUDA失败时尝试回退到CPU
        if use_cuda and not use_cuda_fallback:
            logger.warning("处理异常，尝试回退到CPU处理...")
            return process_video(input_path, output_path, width, height, duration, use_cuda_fallback=True)
        return False


def validate_output(output_path: str, expected_width: int, expected_height: int) -> bool:
    """验证输出文件是否有效"""
    try:
        if not os.path.exists(output_path):
            logger.error(f"输出文件不存在: {output_path}")
            return False

        file_size = os.path.getsize(output_path)
        if file_size < 1024:  # 小于1KB
            logger.error(f"输出文件太小: {file_size} bytes")
            return False

        # 检查视频信息
        video_info = get_video_info(output_path)
        if not video_info:
            logger.warning("无法获取输出视频信息，但仍认为处理成功")
            return True

        # 检查分辨率
        if video_info['width'] != expected_width or video_info['height'] != expected_height:
            logger.warning(
                f"输出分辨率不匹配: 期望 {expected_width}x{expected_height}, 实际 {video_info['width']}x{video_info['height']}")
            # 不严格要求，继续处理

        return True
    except Exception as e:
        logger.warning(f"验证输出文件时出错: {str(e)}")
        return True  # 验证失败但不视为处理失败


def main():
    """主函数"""
    global use_cuda, hardware_acceleration, interrupted

    logger.info("=== 9:16视频比例处理器 (CUDA加速版) ===")
    logger.info(
        f"运行环境: Python {sys.version.split()[0]}, {platform.system()} {platform.release()}")

    # 设置信号处理
    setup_signal_handlers()

    # 检查FFmpeg
    if not check_ffmpeg():
        sys.exit(1)

    # 检查CUDA支持
    check_cuda_support()

    logger.info(f"处理模式: {'CUDA GPU加速' if use_cuda else 'CPU软件'}")

    # 获取当前目录
    current_dir = os.getcwd()
    logger.info(f"当前工作目录: {current_dir}")

    # 创建output目录
    output_dir = os.path.join(current_dir, "output")
    try:
        os.makedirs(output_dir, exist_ok=True)
        logger.info(f"输出目录: {output_dir}")
    except Exception as e:
        logger.error(f"创建输出目录失败: {str(e)}")
        sys.exit(1)

    # 支持的视频扩展名
    video_extensions = ['.mp4', '.avi', '.mov',
                        '.mkv', '.flv', '.wmv', '.webm']

    # 获取所有视频文件
    video_files = []
    for ext in video_extensions:
        pattern = os.path.join(current_dir, f'*{ext}')
        video_files.extend(glob.glob(pattern, recursive=False))
        pattern_upper = os.path.join(current_dir, f'*{ext.upper()}')
        video_files.extend(glob.glob(pattern_upper, recursive=False))

    # 去重
    video_files = list(set([os.path.normpath(f) for f in video_files]))

    if not video_files:
        logger.warning("未找到视频文件。支持的格式: " + ", ".join(video_extensions))
        sys.exit(0)

    logger.info(f"找到 {len(video_files)} 个视频文件进行处理")

    # 显示文件列表
    for i, video_file in enumerate(sorted(video_files), 1):
        file_name = os.path.basename(video_file)
        file_size_mb = get_file_size_mb(video_file)
        logger.info(f"{i}. {file_name} ({file_size_mb:.2f} MB)")

    processed_count = 0
    skipped_count = 0
    error_count = 0
    total_processing_time = 0

    # 处理每个视频文件
    for i, video_file in enumerate(sorted(video_files), 1):
        if interrupted:
            logger.warning(f"处理过程被用户中断，跳过剩余 {len(video_files) - i + 1} 个文件")
            break

        file_name = os.path.basename(video_file)
        logger.info(f"\n{'='*60}")
        logger.info(f"处理文件 {i}/{len(video_files)}: {file_name}")
        logger.info(f"{'='*60}")

        # 检查文件是否存在且可读
        if not os.path.exists(video_file):
            logger.error(f"文件不存在: {video_file}")
            error_count += 1
            continue

        if not os.access(video_file, os.R_OK):
            logger.error(f"无读取权限: {video_file}")
            error_count += 1
            continue

        # 获取视频信息
        video_info = get_video_info(video_file)
        if not video_info:
            logger.error(f"无法获取视频信息: {file_name}")
            error_count += 1
            continue

        width = video_info['width']
        height = video_info['height']
        duration = video_info['duration']

        if width <= 0 or height <= 0:
            logger.error(f"无效的视频分辨率: {width}x{height}")
            error_count += 1
            continue

        # 检查比例
        if is_valid_aspect_ratio(width, height):
            logger.info(f"视频 '{file_name}' 已符合9:16或16:9比例，跳过处理")
            skipped_count += 1
            continue

        # 构建输出路径
        output_file = os.path.join(output_dir, file_name)

        # 检查输出文件是否已存在
        if os.path.exists(output_file):
            try:
                input_size = os.path.getsize(video_file)
                output_size = os.path.getsize(output_file)
                if output_size > input_size * 0.1:  # 输出文件至少是输入文件大小的10%
                    # 验证输出文件是否有效
                    if validate_output(output_file, width, height):
                        logger.info(f"输出文件已存在且有效，跳过: {file_name}")
                        skipped_count += 1
                        continue
                    else:
                        logger.warning(f"输出文件存在但验证失败，重新处理: {file_name}")
                else:
                    logger.warning(
                        f"输出文件已存在但太小 ({output_size} bytes)，重新处理: {file_name}")
            except Exception as e:
                logger.warning(f"检查输出文件时出错，重新处理: {file_name} - {str(e)}")

        # 处理视频
        start_time = time.time()
        if process_video(video_file, output_file, width, height, duration):
            processing_time = time.time() - start_time
            total_processing_time += processing_time
            processed_count += 1
            logger.info(f"单个文件处理耗时: {format_time(processing_time)}")
        else:
            error_count += 1

    # 打印摘要
    logger.info(f"\n{'='*60}")
    logger.info("处理完成摘要")
    logger.info(f"{'='*60}")
    logger.info(f"成功处理: {processed_count} 个文件")
    logger.info(f"已跳过: {skipped_count} 个文件 (符合比例要求或已存在)")
    logger.info(f"处理失败: {error_count} 个文件")

    if processed_count > 0:
        avg_time = total_processing_time / processed_count
        logger.info(f"平均处理时间: {format_time(avg_time)} 每文件")
        logger.info(f"总处理时间: {format_time(total_processing_time)}")

    logger.info(f"输出目录: {output_dir}")
    logger.info(f"日志目录: {os.path.join(current_dir, 'logs')}")

    # 退出状态
    if error_count > 0 and processed_count == 0:
        logger.error("所有文件处理失败")
        sys.exit(1)
    elif interrupted:
        logger.warning("程序因用户中断而退出")
        sys.exit(1)
    else:
        logger.info("[SUCCESS] 处理完成!")
        sys.exit(0)


if __name__ == "__main__":
    try:
        main()
    except KeyboardInterrupt:
        logger.info("程序被用户中断")
        sys.exit(1)
    except Exception as e:
        logger.exception(f"发生未处理的异常: {str(e)}")
        sys.exit(1)
