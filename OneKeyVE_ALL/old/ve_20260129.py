#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
视频批量处理程序（仅处理当前目录层级）
功能：自动处理当前目录（不含子目录）中的所有视频文件
使用：python xxx.py（直接运行即可）
"""

import os
import sys
import json
import logging
from pathlib import Path
from typing import List, Dict
import ffmpeg  # 确保使用ffmpeg-python库

# 设置日志
logging.basicConfig(
    level=logging.INFO,
    format='%(asctime)s - %(levelname)s - %(message)s',
    handlers=[
        logging.FileHandler("video_processing.log", encoding='utf-8'),
        logging.StreamHandler(sys.stdout)
    ]
)
logger = logging.getLogger("VideoProcessor")


def setup_ffmpeg_paths() -> Dict[str, str]:
    """设置FFmpeg路径，基于诊断文件中的信息"""
    try:
        diag_file = Path("ffmpeg_full_diagnostics.json")
        if diag_file.exists():
            with open(diag_file, 'r', encoding='utf-8') as f:
                diag_data = json.load(f)

            ffmpeg_path = diag_data["components"]["ffmpeg"]["path"]
            ffprobe_path = diag_data["components"]["ffprobe"]["path"]

            os.environ["IMAGEIO_FFMPEG_EXE"] = ffmpeg_path
            os.environ["FFMPEG_BINARY"] = ffmpeg_path
            os.environ["FFPROBE_BINARY"] = ffprobe_path

            logger.info(f"成功设置FFmpeg路径: {ffmpeg_path}")
            return {
                "ffmpeg": ffmpeg_path,
                "ffprobe": ffprobe_path
            }
        else:
            logger.warning("未找到ffmpeg_full_diagnostics.json，将使用系统PATH中的FFmpeg")
            return {"ffmpeg": "ffmpeg", "ffprobe": "ffprobe"}
    except Exception as e:
        logger.error(f"设置FFmpeg路径时出错: {str(e)}")
        return {"ffmpeg": "ffmpeg", "ffprobe": "ffprobe"}


class VideoProcessor:
    """视频处理器类"""

    def __init__(self, ffmpeg_paths: Dict[str, str]):
        self.ffmpeg_paths = ffmpeg_paths
        self.output_base_dir = Path("output")

        # 创建输出目录结构
        (self.output_base_dir / "9x20").mkdir(parents=True, exist_ok=True)
        (self.output_base_dir / "5x11").mkdir(parents=True, exist_ok=True)

        # 检查GPU支持
        self.gpu_available = self._check_gpu_support()
        logger.info(
            f"GPU加速状态: {'可用 (CUDA)' if self.gpu_available else '不可用，使用CPU'}")

    def _check_gpu_support(self) -> bool:
        """检查CUDA GPU支持"""
        try:
            # 尝试调用ffmpeg检查硬件加速器
            import subprocess
            result = subprocess.run(
                [self.ffmpeg_paths["ffmpeg"], "-hide_banner", "-hwaccels"],
                capture_output=True,
                text=True,
                timeout=5
            )
            if "cuda" in result.stdout.lower() or "nvdec" in result.stdout.lower():
                logger.info("检测到FFmpeg支持CUDA硬件加速")
                return True
            logger.info("FFmpeg未检测到CUDA支持")
        except Exception as e:
            logger.warning(f"GPU检测失败: {str(e)}")
        return False

    def get_video_info(self, video_path: Path) -> Dict:
        """获取视频基本信息"""
        try:
            probe = ffmpeg.probe(
                str(video_path),
                cmd=self.ffmpeg_paths["ffprobe"]
            )

            video_stream = next(
                (s for s in probe['streams'] if s['codec_type'] == 'video'),
                None
            )

            if not video_stream:
                logger.error(f"文件中无有效视频流: {video_path}")
                return {}

            width = int(video_stream.get('width', 0))
            height = int(video_stream.get('height', 0))

            # 处理旋转元数据
            rotation = 0
            if 'tags' in video_stream and 'rotate' in video_stream['tags']:
                try:
                    rotation = int(video_stream['tags']['rotate'])
                    # 根据旋转调整宽高
                    if rotation in [90, 270]:
                        width, height = height, width
                except:
                    pass

            return {
                'width': width,
                'height': height,
                'aspect_ratio': width / height if height > 0 else 0,
                'rotation_tag': rotation
            }
        except Exception as e:
            logger.error(f"获取视频信息失败 ({video_path}): {str(e)}")
            return {}

    def needs_rotation(self, video_info: Dict) -> bool:
        """判断是否需要顺时针旋转90度（确保宽<高）"""
        # 已处理旋转元数据后的宽高比
        return video_info['aspect_ratio'] > 1.0

    def process_video(self, video_path: Path):
        """处理单个视频文件"""
        try:
            logger.info(f"\n{'='*60}")
            logger.info(f"处理视频: {video_path.name}")
            logger.info(f"{'='*60}")

            # 获取视频信息
            video_info = self.get_video_info(video_path)
            if not video_info or video_info['width'] == 0 or video_info['height'] == 0:
                logger.error(f"跳过无效视频: {video_path}")
                return

            logger.info(f"原始尺寸: {video_info['width']}x{video_info['height']}, "
                        f"宽高比: {video_info['aspect_ratio']:.2f}")

            # 确定是否需要旋转
            rotate = self.needs_rotation(video_info)
            logger.info(f"需要旋转: {'是 (顺时针90°)' if rotate else '否'}")

            # 确定处理后的基础尺寸
            if rotate:
                proc_width = video_info['height']
                proc_height = video_info['width']
            else:
                proc_width = video_info['width']
                proc_height = video_info['height']

            logger.info(f"处理后基础尺寸: {proc_width}x{proc_height}")

            # 处理两种比例
            self._process_ratio(video_path, rotate,
                                proc_width, proc_height, "9x20", 9/20)
            self._process_ratio(video_path, rotate,
                                proc_width, proc_height, "5x11", 5/11)

        except Exception as e:
            logger.error(
                f"处理视频 {video_path.name} 时发生错误: {str(e)}", exc_info=True)

    def _process_ratio(
        self,
        video_path: Path,
        rotate: bool,
        base_width: int,
        base_height: int,
        ratio_name: str,
        target_ratio: float
    ):
        """处理单个目标比例"""
        try:
            # 计算目标高度（宽度保持不变）
            target_height = int(base_width / target_ratio)
            output_dir = self.output_base_dir / ratio_name
            output_path = output_dir / f"{video_path.stem}_{ratio_name}.mp4"

            # 跳过已存在的文件
            if output_path.exists():
                logger.info(f"  ✓ 跳过已存在文件: {ratio_name} ({output_path.name})")
                return

            logger.info(f"  处理 {ratio_name} 比例: {base_width}x{target_height}")

            # 构建输入流
            input_stream = ffmpeg.input(str(video_path))

            # 步骤1: 处理旋转（如需要）
            if rotate:
                video = input_stream.video.filter('transpose', 1)  # 顺时针90度
            else:
                video = input_stream.video

            # 步骤2: 创建背景轨道（轨道1）- 放大+裁剪+模糊
            bg = (
                video
                .filter('scale', base_width, target_height, force_original_aspect_ratio='increase')
                .filter('crop', base_width, target_height)
            )

            # 应用高斯模糊（优先GPU）
            if self.gpu_available:
                try:
                    bg = bg.filter('gblur_cuda', sigma=20)
                except Exception as e:
                    logger.warning(f"GPU模糊失败，回退CPU: {str(e)}")
                    bg = bg.filter('gblur', sigma=20)
            else:
                bg = bg.filter('gblur', sigma=20)

            # 步骤3: 创建前景轨道（轨道2）- 居中+裁剪黑边+羽化
            # 先缩放到目标高度，保持宽高比
            fg = video.filter('scale', -2, target_height)

            # 居中填充到目标尺寸（此时会有黑边）
            fg = fg.filter('pad', base_width, target_height,
                           '(ow-iw)/2', '(oh-ih)/2:black')

            # 创建羽化遮罩（线性渐变，30像素）
            # 使用geq生成alpha通道：边缘30像素线性过渡
            mask = fg.filter(
                'geq',
                'lum=if(lt(X,30), X/30*255, if(gt(X,W-30), (W-X)/30*255, 255)):'
                'cb=128:cr=128',
                'lum=if(lt(Y,30), Y/30*255, if(gt(Y,H-30), (H-Y)/30*255, 255)):'
                'cb=128:cr=128'
            ).filter('format', 'gray')

            # 合并前景和遮罩
            fg = ffmpeg.filter([fg, mask], 'alphamerge')

            # 步骤4: 合成（背景+前景）
            final = ffmpeg.filter([bg, fg], 'overlay')

            # 步骤5: 编码输出
            # 选择编码器（GPU优先）
            vcodec = 'h264_nvenc' if self.gpu_available else 'libx264'
            preset = 'p7' if self.gpu_available else 'slow'

            # 构建输出流
            if input_stream.audio:
                output = ffmpeg.output(
                    final,
                    input_stream.audio,
                    str(output_path),
                    vcodec=vcodec,
                    acodec='aac',
                    video_bitrate='10M',
                    preset=preset,
                    crf=23,
                    movflags='+faststart',
                    vsync='passthrough',
                    **({'gpu': 'any'} if self.gpu_available else {})
                )
            else:
                output = ffmpeg.output(
                    final,
                    str(output_path),
                    vcodec=vcodec,
                    video_bitrate='10M',
                    preset=preset,
                    crf=23,
                    movflags='+faststart',
                    vsync='passthrough',
                    **({'gpu': 'any'} if self.gpu_available else {})
                )

            # 执行处理
            logger.info(f"    编码中... (输出: {output_path.name})")
            output.overwrite_output().run(
                cmd=self.ffmpeg_paths["ffmpeg"],
                capture_stdout=True,
                capture_stderr=True,
                quiet=False
            )

            logger.info(f"  ✓ 完成: {ratio_name} -> {output_path.name}")

        except ffmpeg.Error as e:
            stderr = e.stderr.decode('utf-8') if e.stderr else str(e)
            logger.error(f"FFmpeg处理失败 ({ratio_name}): {stderr}")
            # 清理可能的损坏文件
            if 'output_path' in locals() and Path(output_path).exists():
                try:
                    Path(output_path).unlink()
                except:
                    pass
        except Exception as e:
            logger.error(f"处理 {ratio_name} 时发生错误: {str(e)}", exc_info=True)


def find_video_files_in_current_dir() -> List[Path]:
    """
    仅查找当前目录（不含任何子目录）中的视频文件
    排除output目录本身（如果它在当前目录）
    """
    video_extensions = {'.mp4', '.avi', '.mov',
                        '.mkv', '.flv', '.wmv', '.webm', '.m4v'}
    current_dir = Path('.')
    video_files = []

    # 仅遍历当前目录的直接子项
    for item in current_dir.iterdir():
        # 跳过目录（包括output目录）
        if item.is_dir():
            continue
        # 检查扩展名
        if item.suffix.lower() in video_extensions:
            # 额外安全检查：跳过output目录中的文件（虽然不会出现，因为只遍历当前目录）
            if 'output' in item.parts:
                continue
            video_files.append(item)

    # 按名称排序
    video_files.sort(key=lambda x: x.name.lower())

    logger.info(f"在当前目录找到 {len(video_files)} 个视频文件:")
    for vf in video_files:
        logger.info(f"  - {vf.name}")

    return video_files


def main():
    """主执行流程"""
    try:
        logger.info("\n" + "="*60)
        logger.info("视频批量处理程序启动 (仅处理当前目录)")
        logger.info("="*60 + "\n")

        # 1. 设置FFmpeg路径
        ffmpeg_paths = setup_ffmpeg_paths()

        # 2. 查找当前目录视频文件（不含子目录）
        video_files = find_video_files_in_current_dir()

        if not video_files:
            logger.warning("\n⚠ 未在当前目录找到任何视频文件！")
            logger.info("提示: 程序仅处理当前目录下的视频，不包含子目录")
            return

        # 3. 初始化处理器
        processor = VideoProcessor(ffmpeg_paths)

        # 4. 处理每个视频
        total = len(video_files)
        for idx, video_file in enumerate(video_files, 1):
            logger.info(f"\n[{idx}/{total}] 正在处理: {video_file.name}")
            processor.process_video(video_file)

        logger.info("\n" + "="*60)
        logger.info(f"✓ 所有 {total} 个视频处理完成!")
        logger.info(f"输出目录: output/9x20 和 output/5x11")
        logger.info("="*60)

    except KeyboardInterrupt:
        logger.error("\n⚠ 处理被用户中断")
        sys.exit(1)
    except Exception as e:
        logger.critical(f"\n✗ 程序发生严重错误: {str(e)}", exc_info=True)
        sys.exit(1)


if __name__ == "__main__":
    # 显存限制提示（程序自身不直接控制，但提醒用户）
    logger.info("提示: 本程序设计为单任务处理，显存使用控制在4GB以内")
    logger.info("      如遇显存不足，请减少同时运行的实例数量\n")
    main()
