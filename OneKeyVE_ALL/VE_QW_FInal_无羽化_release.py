#!/usr/bin/env python3
"""
视频自动处理程序 - 双问题终极修复版

功能说明:
1. 自动检测当前目录下所有视频文件
2. 跳过已经是9:16比例的视频
3. 对于其他比例的视频，使用正确的FFmpeg滤镜链处理:
   - 通过subprocess直接调用ffmpeg命令检测硬件加速能力
   - 精确的视频比例处理，消除不必要的黑边
4. 使用指定路径的FFmpeg和GPU加速进行视频处理和编码
5. 输出到output目录，保持原文件名
"""

import os
import sys
import json
import subprocess
import platform
import time
import logging
from pathlib import Path
from typing import Dict, List, Tuple, Optional, Any, Union
import ffmpeg  # type: ignore
import re

# 配置日志
logging.basicConfig(
    level=logging.INFO,
    format='%(asctime)s - %(levelname)s - %(message)s',
    handlers=[
        logging.FileHandler("video_edit.log", encoding='utf-8', mode='w'),
        logging.StreamHandler(sys.stdout)
    ]
)
logger = logging.getLogger(__name__)

# 全局常量
VIDEO_EXTENSIONS = ['.mp4', '.avi', '.mov',
                    '.mkv', '.flv', '.wmv', '.m4v', '.webm']
TARGET_RATIO = 9/16  # 9:16 (0.5625)
MAX_VRAM_USAGE = 4 * 1024 * 1024 * 1024  # 4GB in bytes
OUTPUT_DIR = "output"


class FFmpegManager:
    """管理FFmpeg组件和路径"""

    def __init__(self):
        """初始化FFmpeg管理器"""
        # 获取当前脚本所在目录
        self.base_dir = Path(__file__).parent.absolute()
        # 设置FFmpeg目录为当前目录下的ffmpeg子目录
        self.ffmpeg_dir = self.base_dir / "ffmpeg"
        # 检测操作系统
        self.system = platform.system().lower()
        self.is_windows = self.system == "windows"
        self.executable_suffix = ".exe" if self.is_windows else ""

        # 存储找到的组件路径
        self.components = {}
        # GPU加速支持状态
        self.cuda_support = False

        logger.info(f"🔧 FFmpeg管理器初始化:")
        logger.info(f"  基础目录: {self.base_dir}")
        logger.info(f"  FFmpeg目录: {self.ffmpeg_dir}")
        logger.info(f"  操作系统: {platform.system()} {platform.release()}")

    def find_ffmpeg_components(self) -> Dict[str, Path]:
        """
        查找ffmpeg目录中的所有组件
        
        Returns:
            Dict[str, Path]: 组件名称到路径的映射
        """
        components = {}

        if not self.ffmpeg_dir.exists():
            logger.error(f"❌ FFmpeg目录不存在: {self.ffmpeg_dir}")
            return components

        logger.info(f"🔍 扫描FFmpeg组件: {self.ffmpeg_dir}")

        # 确定搜索目录
        search_dirs = [self.ffmpeg_dir]
        bin_dir = self.ffmpeg_dir / "bin"
        if bin_dir.exists():
            search_dirs.append(bin_dir)

        # 常见的FFmpeg组件
        common_components = ['ffmpeg', 'ffprobe', 'ffplay']

        # 扫描目录
        for search_dir in search_dirs:
            for item in search_dir.iterdir():
                if item.is_file():
                    # Windows: 检查.exe文件
                    if self.is_windows:
                        if item.name.endswith('.exe'):
                            name = item.stem
                            # 优先保留主组件
                            if name in common_components or name not in components:
                                components[name] = item
                    else:
                        # 检查文件是否有可执行权限
                        if os.access(item, os.X_OK) or item.name in common_components:
                            name = item.name
                            if name.endswith(self.executable_suffix):
                                name = name[:-len(self.executable_suffix)]
                            components[name] = item

        # 特别查找主要组件
        for comp in common_components:
            if comp not in components:
                # 尝试在bin目录中查找
                exe_path = bin_dir / f"{comp}{self.executable_suffix}"
                if exe_path.exists():
                    components[comp] = exe_path
                    continue

                # 尝试在根目录查找
                exe_path = self.ffmpeg_dir / f"{comp}{self.executable_suffix}"
                if exe_path.exists():
                    components[comp] = exe_path

        logger.info(f"✅ 找到 {len(components)} 个组件:")
        for name, path in components.items():
            logger.info(f"  - {name}: {path}")

        self.components = components
        return components

    def get_component_path(self, component_name: str) -> Optional[Path]:
        """
        获取指定组件的路径
        
        Args:
            component_name: 组件名称 (如 'ffmpeg', 'ffprobe')
        
        Returns:
            Optional[Path]: 组件路径，如果找不到则返回None
        """
        if not self.components:
            self.find_ffmpeg_components()

        return self.components.get(component_name.lower())

    def has_cuda_support(self) -> bool:
        """
        检查FFmpeg是否支持CUDA加速 - 修复版
        
        Returns:
            bool: 是否支持CUDA
        """
        logger.info("🔍 检查CUDA加速支持...")

        ffmpeg_path = self.get_component_path('ffmpeg')
        if not ffmpeg_path:
            logger.error("❌ 未找到ffmpeg组件")
            self.cuda_support = False
            return False

        try:
            # 通过执行命令检查编码器支持 - 安全可靠的方式
            cmd = [str(ffmpeg_path), '-hide_banner', '-encoders']
            result = subprocess.run(
                cmd, capture_output=True, text=True, check=True)

            # 检查输出中是否包含CUDA/NVENC支持
            output = result.stdout.lower()

            cuda_support = 'cuvid' in output or 'cuda' in output
            nvenc_support = 'nvenc' in output

            logger.info("✅ CUDA加速支持检查完成:")
            logger.info(f"  CUDA解码: {'✅ 支持' if cuda_support else '❌ 不支持'}")
            logger.info(f"  NVENC编码: {'✅ 支持' if nvenc_support else '❌ 不支持'}")

            self.cuda_support = cuda_support or nvenc_support
            return self.cuda_support

        except Exception as e:
            logger.error(f"❌ 检查CUDA支持时出错: {str(e)}")
            self.cuda_support = False
            return False

    def get_video_info(self, video_path: Path) -> Dict[str, Any]:
        """
        获取视频信息，使用指定路径的ffprobe
        
        Args:
            video_path: 视频文件路径
        
        Returns:
            Dict[str, Any]: 包含视频信息的字典
        """
        try:
            ffprobe_path = self.get_component_path('ffprobe')
            if not ffprobe_path:
                logger.error("❌ 未找到ffprobe组件")
                return {}

            # 使用ffprobe获取视频信息
            probe = ffmpeg.probe(
                str(video_path),
                cmd=str(ffprobe_path)
            )

            # 找到视频流
            video_stream = next(
                (stream for stream in probe['streams']
                 if stream['codec_type'] == 'video'),
                None
            )

            if not video_stream:
                logger.error(f"❌ 未找到视频流: {video_path}")
                return {}

            # 计算实际宽高比，考虑像素长宽比
            width = int(video_stream['width'])
            height = int(video_stream['height'])

            # 处理像素长宽比 (SAR)
            sar_ratio = 1.0
            if 'sample_aspect_ratio' in video_stream and video_stream['sample_aspect_ratio'] != '0:1':
                try:
                    sar_num, sar_den = map(
                        int, video_stream['sample_aspect_ratio'].split(':'))
                    sar_ratio = sar_num / sar_den
                except (ValueError, ZeroDivisionError):
                    sar_ratio = 1.0

            # 计算显示宽高比 (DAR)
            dar_numerator = width * sar_ratio
            dar_denominator = height
            display_ratio = dar_numerator / dar_denominator

            # 计算原始宽高比
            original_ratio = width / height

            # 获取帧率
            fps = 30.0
            if 'avg_frame_rate' in video_stream and video_stream['avg_frame_rate'] != '0/0':
                try:
                    num, den = map(
                        int, video_stream['avg_frame_rate'].split('/'))
                    if den != 0:
                        fps = num / den
                except (ValueError, ZeroDivisionError):
                    fps = 30.0

            # 检查是否有音频流
            has_audio = any(stream['codec_type'] ==
                            'audio' for stream in probe['streams'])

            # 检查音频编解码器
            audio_codec = None
            for stream in probe['streams']:
                if stream['codec_type'] == 'audio':
                    audio_codec = stream.get('codec_name', '')
                    break

            video_info = {
                'width': width,
                'height': height,
                'original_ratio': original_ratio,
                'display_ratio': display_ratio,
                'sar_ratio': sar_ratio,
                'duration': float(probe['format']['duration']) if 'duration' in probe['format'] and probe['format']['duration'] != 'N/A' else 0,
                'bit_rate': int(probe['format']['bit_rate']) if 'bit_rate' in probe['format'] else 0,
                'codec_name': video_stream.get('codec_name', ''),
                'fps': fps,
                'has_audio': has_audio,
                'audio_codec': audio_codec
            }

            logger.debug(
                f"📊 视频信息 - {video_path.name}: {json.dumps(video_info, indent=2)}")
            return video_info

        except ffmpeg.Error as e:
            stderr = e.stderr.decode() if e.stderr else str(e)
            logger.error(f"❌ FFprobe错误获取视频信息 {video_path}: {stderr}")
        except Exception as e:
            logger.error(f"❌ 获取视频信息失败 {video_path}: {str(e)}")
            logger.exception("详细错误信息:")

        return {}

    def process_video(self, input_path: Path, output_path: Path, target_width: int, target_height: int, use_cuda: bool = False) -> bool:
        """
        使用正确的FFmpeg滤镜链处理单个视频文件
        
        Args:
            input_path: 输入视频路径
            output_path: 输出视频路径
            target_width: 目标宽度
            target_height: 目标高度
            use_cuda: 是否使用CUDA加速
        
        Returns:
            bool: 处理是否成功
        """
        try:
            ffmpeg_path = self.get_component_path('ffmpeg')
            if not ffmpeg_path:
                logger.error("❌ 未找到ffmpeg组件")
                return False

            # 获取视频信息
            video_info = self.get_video_info(input_path)
            if not video_info:
                logger.error(f"❌ 无法获取视频信息: {input_path}")
                return False

            orig_w, orig_h = video_info['width'], video_info['height']
            has_audio = video_info['has_audio']
            original_ratio = video_info['display_ratio']

            logger.info(f"\n{'='*60}")
            logger.info(f"🎥 处理视频: {input_path.name}")
            logger.info(
                f"🎯 原始分辨率: {orig_w}x{orig_h} (比例: {original_ratio:.4f})")
            logger.info(f"🎯 目标分辨率: {target_width}x{target_height}")
            logger.info(f"🚀 {'使用CUDA加速' if use_cuda else '使用CPU处理'}")

            # 创建输入流
            input_stream = ffmpeg.input(str(input_path))

            # 修复1: 正确使用split滤镜 - 避免"multiple outgoing edges"错误
            # 将输入流分成两个副本，一个用于背景，一个用于前景
            split_streams = input_stream.video.filter_multi_output('split')

            # 修复2: 根据原始比例动态调整处理逻辑
            # 背景流: 放大以填充整个目标区域，然后模糊
            bg = (
                split_streams[0]
                .filter('scale', w=target_width, h=target_height, force_original_aspect_ratio='increase')
                .filter('crop', target_width, target_height)
                .filter('gblur', sigma=15)
            )

            # 前景流: 精确处理不同比例的视频
            if original_ratio > TARGET_RATIO:  # 宽视频 (如16:9, 1:1)
                # 保持宽度，高度按比例缩放，然后垂直居中
                fg = (
                    split_streams[1]
                    .filter('scale', w=target_width, h=-2)
                    .filter('pad', w=target_width, h=target_height, x='(ow-iw)/2', y='(oh-ih)/2', color='black@0')
                )
            else:  # 高视频 (如9:16原生, 3:4)
                # 保持高度，宽度按比例缩放，然后水平居中
                fg = (
                    split_streams[1]
                    .filter('scale', w=-2, h=target_height)
                    .filter('pad', w=target_width, h=target_height, x='(ow-iw)/2', y='(oh-ih)/2', color='black@0')
                )

            # 修复3: 将前景精确叠加到背景，避免黑边
            output_video = bg.overlay(fg)

            # 根据CUDA支持选择编码器
            output_args = {}
            if use_cuda:
                logger.info("⚡ 启用NVIDIA GPU硬件加速编码")
                output_args.update({
                    'c:v': 'h264_nvenc',
                    'preset': 'p7',
                    'profile:v': 'main',
                    'b:v': '8M',
                    'maxrate': '10M',
                    'bufsize': '16M',
                    'rc': 'vbr_hq',
                })
            else:
                output_args.update({
                    'c:v': 'libx264',
                    'preset': 'slow',
                    'crf': '23',
                    'movflags': '+faststart'
                })

            # 仅当有音频流时才添加音频参数
            if has_audio:
                # 检查音频编解码器是否支持
                audio_codec = video_info.get('audio_codec', '')
                if audio_codec in ['aac', 'mp3', 'opus', 'ac3']:
                    # 保留原始音频
                    output_args.update({
                        'c:a': 'copy'
                    })
                    logger.info("🔊 保留原始音频流 (直接复制)")
                else:
                    # 重新编码为AAC
                    output_args.update({
                        'c:a': 'aac',
                        'b:a': '128k'
                    })
                    logger.info("🔊 重新编码音频为AAC格式")

            logger.info(f"⚙️ 构建FFmpeg命令...")
            start_time = time.time()

            # 构建输出
            output = output_video.output(
                str(output_path),
                **output_args
            )

            # 执行命令
            logger.info("🚀 开始视频处理...")
            output.run(
                cmd=str(ffmpeg_path),
                overwrite_output=True,
                capture_stdout=True,
                capture_stderr=True
            )

            elapsed_time = time.time() - start_time

            logger.info(f"✅ 视频处理成功! 耗时: {elapsed_time:.2f}秒")
            if output_path.exists():
                output_size = output_path.stat().st_size / (1024 * 1024)
                logger.info(f"💾 输出文件: {output_path}")
                logger.info(f"📊 输出文件大小: {output_size:.2f} MB")

                # 检查输出是否合理
                if output_size < 0.1:  # 小于100KB，可能有问题
                    logger.warning("⚠️ 输出文件异常小，可能存在处理问题")
            else:
                logger.error(f"❌ 输出文件未创建: {output_path}")
                return False

            return True

        except ffmpeg.Error as e:
            # 专门处理FFmpeg错误
            stderr = e.stderr.decode(
                'utf-8', errors='replace') if e.stderr else str(e)
            logger.error(f"❌ FFmpeg处理失败 ({input_path}):")
            logger.error(f"标准错误: {stderr}")
            return False
        except Exception as e:
            logger.error(f"❌ 处理视频时出错 ({input_path}): {str(e)}")
            logger.exception("详细错误信息:")
            return False


# 全局FFmpeg管理器实例
ffmpeg_manager = FFmpegManager()


def setup_environment() -> None:
    """
    设置运行环境，检查依赖项
    """
    logger.info("🔧 设置运行环境...")

    # 检查ffmpeg-python库
    try:
        import ffmpeg
        logger.info("✅ ffmpeg-python 库可用")
    except ImportError:
        logger.error("❌ 未安装 ffmpeg-python 库，请运行: pip install ffmpeg-python")
        sys.exit(1)

    # 检查FFmpeg组件
    components = ffmpeg_manager.find_ffmpeg_components()

    if not components:
        logger.error("❌ 未找到任何FFmpeg组件")
        logger.error("请确保在当前目录下有ffmpeg子目录，并包含以下文件:")
        logger.error("  - Windows: bin/ffmpeg.exe, bin/ffprobe.exe")
        logger.error("  - Linux/Mac: bin/ffmpeg, bin/ffprobe")
        sys.exit(1)

    # 检查必要的组件
    required_components = ['ffmpeg', 'ffprobe']
    missing_components = [
        comp for comp in required_components if comp not in components]

    if missing_components:
        logger.error(f"❌ 缺少必要的FFmpeg组件: {', '.join(missing_components)}")
        sys.exit(1)

    logger.info("✅ 所有必要的FFmpeg组件都已找到")

    # 检查CUDA支持 - 使用修复后的方法
    cuda_support = ffmpeg_manager.has_cuda_support()
    if cuda_support:
        logger.info("✅ 检测到CUDA加速支持")
    else:
        logger.warning("⚠️ 未检测到CUDA加速支持，将使用CPU处理")

    # 确保output目录存在
    os.makedirs(OUTPUT_DIR, exist_ok=True)
    logger.info(f"📁 确保输出目录存在: {OUTPUT_DIR}")


def get_video_files() -> List[Path]:
    """
    获取当前目录下所有视频文件
    
    Returns:
        List[Path]: 视频文件路径列表
    """
    current_dir = Path.cwd()
    video_files = []

    for ext in VIDEO_EXTENSIONS:
        for file in current_dir.glob(f"*{ext}"):
            if file.is_file() and file.stat().st_size > 0:  # 确保文件不为空
                video_files.append(file)

    logger.info(f"🎬 找到 {len(video_files)} 个视频文件:")
    for file in video_files:
        size_mb = file.stat().st_size / (1024 * 1024)
        logger.info(f"  - {file.name} ({size_mb:.2f} MB)")

    return video_files


def is_target_ratio(ratio: float) -> bool:
    """
    检查视频纵横比是否为目标比例(9:16)
    
    Args:
        ratio: 视频纵横比
    
    Returns:
        bool: 是否为目标比例
    """
    # 允许一定的误差范围
    tolerance = 0.01
    target_ratio = TARGET_RATIO

    if abs(ratio - target_ratio) < tolerance:
        logger.debug(f"🎯 检测到9:16比例 (计算值: {ratio:.4f})")
        return True

    # 额外检查: 如果接近9:18或9:15也视为目标比例
    if abs(ratio - (9/18)) < tolerance or abs(ratio - (9/15)) < tolerance:
        logger.debug(f"🎯 检测到接近9:16的特殊比例 (计算值: {ratio:.4f})")
        return True

    return False


def calculate_target_resolution(original_width: int, original_height: int, original_ratio: float) -> Tuple[int, int]:
    """
    计算目标分辨率，保持9:16比例
    
    Args:
        original_width: 原视频宽度
        original_height: 原视频高度
        original_ratio: 原视频纵横比
    
    Returns:
        Tuple[int, int]: 目标分辨率(宽度, 高度)
    """
    # 9:16 = 0.5625
    target_ratio = TARGET_RATIO

    # 始终使用1080x1920作为目标分辨率
    target_width = 1080
    target_height = 1920

    logger.info(
        f"✅ 确定目标分辨率: {target_width}x{target_height} (比例: {target_ratio:.4f})")
    return target_width, target_height


def estimate_vram_usage(width: int, height: int, duration: float, fps: float) -> int:
    """
    估算处理视频所需的VRAM
    
    Args:
        width: 视频宽度
        height: 视频高度
        duration: 视频时长(秒)
        fps: 帧率
    
    Returns:
        int: 估算的VRAM使用量(字节)
    """
    # 估算每帧内存使用 (RGB格式)
    bytes_per_frame = width * height * 3
    # 估算处理所需帧数 (通常是缓冲2秒)
    frames_needed = min(60, int(fps * 2))  # 最多60帧
    # 总估算 (2倍额外开销)
    estimated_vram = bytes_per_frame * frames_needed * 2

    logger.debug(f"📊 估算VRAM使用: {estimated_vram/(1024*1024):.2f} MB "
                 f"(分辨率: {width}x{height}, 缓冲帧数: {frames_needed})")

    return estimated_vram


def process_single_video(input_path: Path) -> bool:
    """
    处理单个视频文件的主函数
    
    Args:
        input_path: 输入视频文件路径
    
    Returns:
        bool: 处理是否成功
    """
    logger.info(f"\n{'='*80}")
    logger.info(f"🎬 处理文件: {input_path.name}")

    # 0. 获取视频信息
    video_info = ffmpeg_manager.get_video_info(input_path)
    if not video_info:
        logger.error(f"❌ 无法获取视频信息: {input_path}")
        return False

    # 1. 检查是否为9:16比例
    original_ratio = video_info.get(
        'display_ratio', video_info.get('original_ratio', 0))
    logger.info(
        f"📏 原始视频比例: {original_ratio:.4f} ({video_info['width']}x{video_info['height']})")

    if is_target_ratio(original_ratio):
        logger.info(f"⏭️ 跳过 {input_path.name}，已经是目标比例")
        # 修复：不再复制文件，直接返回成功
        return True

    # 2-4. 计算目标分辨率
    target_width, target_height = calculate_target_resolution(
        video_info['width'],
        video_info['height'],
        original_ratio
    )

    # 5-6. 处理视频并导出
    output_path = Path(OUTPUT_DIR) / input_path.name

    # 使用全局已检测的CUDA支持状态
    use_cuda = ffmpeg_manager.cuda_support

    # 估算VRAM使用
    duration = video_info.get('duration', 0)
    fps = video_info.get('fps', 30.0)
    estimated_vram = estimate_vram_usage(
        video_info['width'],
        video_info['height'],
        duration,
        fps
    )

    if use_cuda and estimated_vram > MAX_VRAM_USAGE:
        logger.warning(f"⚠️ 估算VRAM使用 ({estimated_vram/(1024*1024*1024):.2f}GB) "
                       f"超过限制 ({MAX_VRAM_USAGE/(1024*1024*1024):.2f}GB)")
        logger.warning("⚠️ 禁用CUDA加速以避免显存溢出")
        use_cuda = False

    return ffmpeg_manager.process_video(
        input_path,
        output_path,
        target_width,
        target_height,
        use_cuda
    )


def process_all_videos() -> None:
    """
    处理所有视频文件
    """
    logger.info("🚀 开始处理所有视频文件")

    # 获取所有视频文件
    video_files = get_video_files()

    if not video_files:
        logger.warning("⚠️ 未找到任何视频文件")
        return

    # 处理每个视频文件
    success_count = 0
    total_count = len(video_files)

    for i, video_file in enumerate(video_files, 1):
        logger.info(f"\n{'='*80}")
        logger.info(f"🔄 处理进度: {i}/{total_count}")

        try:
            # 处理视频
            if process_single_video(video_file):
                success_count += 1
            else:
                logger.error(f"❌ 处理失败: {video_file.name}")

        except Exception as e:
            logger.error(f"❌ 处理 {video_file.name} 时发生未预期错误: {str(e)}")
            logger.exception("详细错误信息:")

    # 总结
    logger.info(f"\n{'='*80}")
    logger.info("📊 处理总结:")
    logger.info(f"✅ 成功处理: {success_count}/{total_count} 个视频")
    logger.info(f"📁 输出目录: {os.path.abspath(OUTPUT_DIR)}")

    if success_count < total_count:
        logger.warning("⚠️ 部分视频处理失败，请查看日志了解详情")
    else:
        logger.info("🎉 所有视频处理成功！")

    logger.info("✅ 所有视频处理完成!")


def main() -> None:
    """
    主函数
    """
    try:
        # 设置环境
        setup_environment()

        # 处理所有视频
        process_all_videos()

    except KeyboardInterrupt:
        logger.info("\n🛑 操作被用户中断")
        sys.exit(1)
    except Exception as e:
        logger.exception(f"💥 严重错误: {str(e)}")
        sys.exit(1)


if __name__ == "__main__":
    main()
