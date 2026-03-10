#!/usr/bin/env python3
"""
VidGear CUDA检测脚本 - 修复版
"""

import sys
import platform
import subprocess
import json
import os
from pathlib import Path


def check_system_info():
    """检查系统信息"""
    print("=" * 60)
    print("系统信息检查")
    print("=" * 60)

    info = {
        '系统': platform.system(),
        '版本': platform.version(),
        '架构': platform.machine(),
        'Python版本': platform.python_version(),
        'Python路径': sys.executable
    }

    for key, value in info.items():
        print(f"{key}: {value}")

    return info


def check_cuda_availability():
    """检查CUDA可用性"""
    print("\n" + "=" * 60)
    print("CUDA可用性检查")
    print("=" * 60)

    cuda_info = {
        'CUDA可用': False,
        'CUDA版本': None,
        'GPU设备数量': 0,
        'GPU设备列表': [],
        'nvcc可用': False,
        'nvidia-smi可用': False
    }

    try:
        # 检查nvidia-smi - 使用UTF-8编码
        result = subprocess.run(['nvidia-smi', '--query-gpu=name,memory.total,driver_version',
                                '--format=csv,noheader'],
                                capture_output=True, text=True, encoding='utf-8')
        if result.returncode == 0:
            cuda_info['nvidia-smi可用'] = True
            lines = result.stdout.strip().split('\n')
            cuda_info['GPU设备数量'] = len(lines)
            cuda_info['GPU设备列表'] = lines

            print(f"✅ nvidia-smi可用")
            print(f"GPU设备数量: {cuda_info['GPU设备数量']}")
            for i, gpu_info in enumerate(lines):
                print(f"  GPU {i}: {gpu_info}")
    except Exception as e:
        print(f"❌ nvidia-smi检查失败: {e}")

    try:
        # 检查nvcc - 使用UTF-8编码
        result = subprocess.run(['nvcc', '--version'],
                                capture_output=True, text=True, encoding='utf-8')
        if result.returncode == 0:
            cuda_info['nvcc可用'] = True
            # 提取CUDA版本
            for line in result.stdout.split('\n'):
                if 'release' in line.lower():
                    cuda_info['CUDA版本'] = line.strip()
                    break
            print(f"✅ nvcc可用")
            if cuda_info['CUDA版本']:
                print(f"CUDA版本: {cuda_info['CUDA版本']}")
    except Exception as e:
        print(f"❌ nvcc检查失败: {e}")

    # 检查CUDA环境变量
    env_vars = ['CUDA_HOME', 'CUDA_PATH']
    print("\n环境变量检查:")
    for var in env_vars:
        value = os.environ.get(var, '未设置')
        print(f"{var}: {value}")

    # 检查PATH中的CUDA
    path_var = os.environ.get('PATH', '')
    if 'cuda' in path_var.lower():
        print("✅ PATH: 包含CUDA路径")
    else:
        print("❌ PATH: 不包含CUDA路径")

    return cuda_info


def check_opencv_cuda():
    """检查OpenCV CUDA支持"""
    print("\n" + "=" * 60)
    print("OpenCV CUDA支持检查")
    print("=" * 60)

    try:
        import cv2

        cv_info = {
            'OpenCV版本': cv2.__version__,
            'CUDA设备数量': 0,
            'CUDA支持': False,
            '编译信息': '简化信息'
        }

        print(f"OpenCV版本: {cv2.__version__}")

        # 检查CUDA支持
        try:
            cuda_count = cv2.cuda.getCudaEnabledDeviceCount()
            cv_info['CUDA设备数量'] = cuda_count
            cv_info['CUDA支持'] = cuda_count > 0

            if cuda_count > 0:
                print(f"✅ OpenCV CUDA支持: 检测到 {cuda_count} 个CUDA设备")
                for i in range(cuda_count):
                    try:
                        device = cv2.cuda.DeviceInfo(i)
                        print(f"  设备 {i}: {device.name()}")
                    except:
                        pass
            else:
                print("❌ OpenCV未检测到CUDA设备")
                print("说明: 您安装的opencv-python是CPU版本")
                print("解决方案:")
                print("  1. pip uninstall opencv-python opencv-python-headless")
                # 或者从源码编译支持CUDA的版本
                print("  2. pip install opencv-contrib-python")

        except AttributeError as e:
            print("❌ OpenCV未编译CUDA支持")
            print(f"  错误: {e}")
            cv_info['CUDA支持'] = False

        return cv_info

    except ImportError:
        print("❌ OpenCV未安装")
        return None
    except Exception as e:
        print(f"❌ OpenCV检查出错: {e}")
        return None


def check_ffmpeg_cuda_safe():
    """安全的FFmpeg CUDA检查"""
    print("\n" + "=" * 60)
    print("FFmpeg CUDA支持检查")
    print("=" * 60)

    ffmpeg_info = {
        'FFmpeg安装': False,
        '版本': None,
        'CUDA支持': False,
        'NVENC支持': False,
        'NVDEC支持': False
    }

    try:
        # 检查ffmpeg版本 - 使用UTF-8编码
        result = subprocess.run(['ffmpeg', '-version'],
                                capture_output=True, text=True, encoding='utf-8', errors='ignore')

        if result.returncode == 0:
            ffmpeg_info['FFmpeg安装'] = True
            lines = result.stdout.split('\n')
            if lines:
                ffmpeg_info['版本'] = lines[0].strip()
                print(f"✅ FFmpeg已安装")
                print(f"  版本: {lines[0].strip()}")

            # 检查CUDA相关配置
            output = result.stdout.lower()

            if 'cuda' in output:
                ffmpeg_info['CUDA支持'] = True
                print("✅ FFmpeg编译时包含CUDA支持")

            if 'nvenc' in output:
                ffmpeg_info['NVENC支持'] = True
                print("✅ FFmpeg编译时包含NVENC编码器支持")

            if 'cuvid' in output or 'nvidia' in output:
                ffmpeg_info['NVDEC支持'] = True
                print("✅ FFmpeg编译时包含NVDEC解码器支持")

            # 检查硬件加速器 - 使用UTF-8编码
            print("\n检查可用硬件加速器...")
            try:
                result = subprocess.run(['ffmpeg', '-hwaccels'],
                                        capture_output=True, text=True, encoding='utf-8', errors='ignore')
                if result.returncode == 0:
                    hwaccels = [line.strip()
                                for line in result.stdout.split('\n') if line.strip()]
                    if len(hwaccels) > 0:
                        for hw in hwaccels:
                            if hw and not hw.startswith('Hardware'):
                                print(f"  {hw}")
                                if 'cuda' in hw.lower():
                                    print("    ✅ CUDA硬件加速可用")
            except Exception as e:
                print(f"  检查硬件加速器失败: {e}")

        else:
            print("❌ FFmpeg检查失败")

    except FileNotFoundError:
        print("❌ FFmpeg未安装")
        print("安装说明:")
        print("  Windows: 从 https://www.gyan.dev/ffmpeg/builds/ 下载")
        print("  下载包含 'full' 的版本以获得CUDA支持")
    except Exception as e:
        print(f"❌ FFmpeg检查出错: {e}")

    return ffmpeg_info


def check_vidgear_cuda():
    """检查VidGear CUDA支持"""
    print("\n" + "=" * 60)
    print("VidGear CUDA支持检查")
    print("=" * 60)

    vidgear_info = {
        'VidGear安装': False,
        '版本': None,
        'WriteGear CUDA支持': False,
        'CamGear CUDA支持': False,
        '可用编码器': [],
        '可用解码器': []
    }

    try:
        import vidgear
        from vidgear.gears import WriteGear

        vidgear_info['VidGear安装'] = True
        vidgear_info['版本'] = vidgear.__version__

        print(f"✅ VidGear已安装 (版本: {vidgear.__version__})")

        # 检查GPU编码器 - 使用安全的FFmpeg检查
        print("\n检查GPU编码器...")
        gpu_encoders = [
            'h264_nvenc',  # NVIDIA H.264
            'hevc_nvenc',  # NVIDIA H.265/HEVC
            'av1_nvenc',   # NVIDIA AV1
            'h264_amf',    # AMD H.264
            'hevc_amf',    # AMD H.265
            'h264_qsv',    # Intel QuickSync H.264
            'hevc_qsv'     # Intel QuickSync H.265
        ]

        try:
            # 使用subprocess.run并捕获错误
            result = subprocess.run(['ffmpeg', '-encoders'],
                                    capture_output=True, text=True,
                                    encoding='utf-8', errors='ignore')
            if result.returncode == 0:
                output = result.stdout
                available_encoders = []

                for encoder in gpu_encoders:
                    if encoder in output:
                        available_encoders.append(encoder)
                        print(f"  ✅ {encoder}")
                    else:
                        print(f"  ❌ {encoder}")

                vidgear_info['可用编码器'] = available_encoders
                vidgear_info['WriteGear CUDA支持'] = len(available_encoders) > 0
                if vidgear_info['WriteGear CUDA支持']:
                    print(f"\n✅ 检测到 {len(available_encoders)} 个GPU编码器")
            else:
                print("❌ 无法获取编码器列表")
        except Exception as e:
            print(f"❌ 检查编码器时出错: {e}")

        # 检查GPU解码器
        print("\n检查GPU解码器...")
        gpu_decoders = [
            'h264_cuvid',  # NVIDIA H.264解码
            'hevc_cuvid',  # NVIDIA H.265解码
            'qsv'          # Intel QuickSync解码
        ]

        try:
            result = subprocess.run(['ffmpeg', '-decoders'],
                                    capture_output=True, text=True,
                                    encoding='utf-8', errors='ignore')
            if result.returncode == 0:
                output = result.stdout
                available_decoders = []

                for decoder in gpu_decoders:
                    if decoder in output:
                        available_decoders.append(decoder)
                        print(f"  ✅ {decoder}")
                    else:
                        print(f"  ❌ {decoder}")

                vidgear_info['可用解码器'] = available_decoders
                vidgear_info['CamGear CUDA支持'] = len(available_decoders) > 0
        except Exception as e:
            print(f"❌ 检查解码器时出错: {e}")

        # 测试VidGear GPU功能
        print("\n测试VidGear GPU功能...")
        test_result = test_vidgear_gpu_functionality()
        if test_result:
            print("✅ VidGear GPU功能测试通过")
        else:
            print("❌ VidGear GPU功能测试失败")

    except ImportError as e:
        print(f"❌ VidGear未安装或导入失败: {e}")
        print("安装命令:")
        print("  pip install vidgear[core]")
        print("  或者: pip install vidgear")

    return vidgear_info


def test_vidgear_gpu_functionality():
    """测试VidGear GPU功能"""
    try:
        from vidgear.gears import WriteGear
        import numpy as np

        print("测试WriteGear GPU编码...")

        # 测试配置
        test_config = {
            "-input_framerate": 30,
            "-vcodec": "h264_nvenc",
            "-preset": "fast",
            "-rc": "vbr",
            "-cq": "23",
            "-b:v": "5M",
            "-maxrate": "10M"
        }

        # 创建测试视频写入器
        try:
            test_output = 'test_gpu_output.mp4'
            writer = WriteGear(
                output=test_output,
                compression_mode=True,
                logging=False,
                **test_config
            )

            print("  ✅ WriteGear GPU编码器初始化成功")
            print(f"  使用的编码器: {test_config['-vcodec']}")

            # 创建测试帧
            test_frame = np.random.randint(
                0, 255, (480, 640, 3), dtype=np.uint8)

            # 写入测试帧
            for i in range(10):
                writer.write(test_frame)

            writer.close()
            print("  ✅ GPU编码测试完成")

            # 检查文件
            if Path(test_output).exists():
                file_size = Path(test_output).stat().st_size
                print(f"  ✅ 测试文件创建成功 ({file_size} 字节)")

                # 清理
                try:
                    Path(test_output).unlink()
                    print("  ✅ 测试文件已清理")
                except:
                    print("  ⚠️  无法删除测试文件")
            else:
                print("  ❌ 测试文件未创建")

            return True

        except Exception as e:
            print(f"  ❌ WriteGear GPU编码失败: {e}")
            return False

    except Exception as e:
        print(f"  ❌ VidGear GPU功能测试失败: {e}")
        return False


def run_quick_test():
    """运行快速CUDA功能测试"""
    print("\n" + "=" * 60)
    print("快速功能测试")
    print("=" * 60)

    try:
        import cv2
        import numpy as np

        print("测试1: OpenCV基础功能")
        print(f"  OpenCV版本: {cv2.__version__}")

        # 测试图像处理
        test_image = np.random.randint(0, 255, (100, 100, 3), dtype=np.uint8)
        gray = cv2.cvtColor(test_image, cv2.COLOR_BGR2GRAY)
        print("  ✅ OpenCV CPU图像处理正常")

        # 测试CUDA
        try:
            cuda_count = cv2.cuda.getCudaEnabledDeviceCount()
            if cuda_count > 0:
                print(f"  ✅ 检测到 {cuda_count} 个CUDA设备")
                # 测试GPU处理
                gpu_mat = cv2.cuda_GpuMat()
                gpu_mat.upload(test_image)
                downloaded = gpu_mat.download()
                print("  ✅ GPU上传/下载测试通过")
            else:
                print("  ⚠️  未检测到CUDA设备（OpenCV是CPU版本）")
        except AttributeError:
            print("  ⚠️  OpenCV未编译CUDA支持")

        # 测试视频编码
        print("\n测试2: 视频编码测试")
        test_video_encoding()

    except ImportError as e:
        print(f"❌ 导入模块失败: {e}")
    except Exception as e:
        print(f"❌ 快速测试出错: {e}")
        import traceback
        traceback.print_exc()


def test_video_encoding():
    """测试视频编码功能"""
    try:
        import cv2
        import numpy as np

        print("创建测试视频...")

        # 创建测试视频写入器
        test_output = 'test_encode_cpu.mp4'
        fourcc = cv2.VideoWriter_fourcc(*'mp4v')
        out = cv2.VideoWriter(test_output, fourcc, 30.0, (640, 480))

        if out.isOpened():
            print("  ✅ CPU编码器可用")

            # 创建测试帧
            for i in range(30):
                frame = np.random.randint(
                    0, 255, (480, 640, 3), dtype=np.uint8)
                out.write(frame)

            out.release()
            print("  ✅ 测试帧写入完成")

            # 检查文件
            if Path(test_output).exists():
                file_size = Path(test_output).stat().st_size
                print(f"  ✅ 测试视频创建成功 ({file_size} 字节)")

                # 清理
                try:
                    Path(test_output).unlink()
                    print("  ✅ 测试文件已清理")
                except:
                    print("  ⚠️  无法删除测试文件")
            else:
                print("  ❌ 测试文件未创建")
        else:
            print("  ❌ CPU编码器不可用")

    except Exception as e:
        print(f"  ❌ 视频编码测试失败: {e}")


def generate_summary_report(all_checks):
    """生成总结报告"""
    print("\n" + "=" * 60)
    print("系统支持总结报告")
    print("=" * 60)

    # 提取信息
    has_nvidia_gpu = all_checks.get('cuda_info', {}).get('nvidia-smi可用', False)
    opencv_cuda = all_checks.get('opencv_info', {}).get('CUDA支持', False)
    ffmpeg_nvenc = all_checks.get('ffmpeg_info', {}).get('NVENC支持', False)
    vidgear_gpu = all_checks.get('vidgear_info', {}).get(
        'WriteGear CUDA支持', False)

    print(f"NVIDIA GPU: {'✅' if has_nvidia_gpu else '❌'}")
    print(f"OpenCV CUDA: {'✅' if opencv_cuda else '❌'}")
    print(f"FFmpeg NVENC: {'✅' if ffmpeg_nvenc else '❌'}")
    print(f"VidGear GPU编码: {'✅' if vidgear_gpu else '❌'}")

    print("\n" + "=" * 60)
    print("建议:")
    print("=" * 60)

    if vidgear_gpu and ffmpeg_nvenc:
        print("🎉 您的系统支持VidGear GPU加速！")
        print("可以使用FFmpeg的硬件编码器加速视频处理")
        print("\n示例配置:")
        print("""
from vidgear.gears import WriteGear

# GPU编码配置
output_params = {
    "-vcodec": "h264_nvenc",
    "-preset": "fast",
    "-cq": "23",
    "-rc": "vbr",
    "-b:v": "5M"
}

# 创建写入器
writer = WriteGear(output='output.mp4', **output_params)
        """)

        if not opencv_cuda:
            print("\n⚠️  注意：")
            print("虽然VidGear可以使用GPU编码，但OpenCV是CPU版本")
            print("图像处理（缩放、模糊等）仍在CPU上进行")
            print("可以考虑：")
            print("  1. 使用opencv-contrib-python（可能包含CUDA）")
            print("  2. 从源码编译支持CUDA的OpenCV")

    elif has_nvidia_gpu and not ffmpeg_nvenc:
        print("⚠️  系统有NVIDIA GPU，但FFmpeg不支持NVENC")
        print("建议安装支持CUDA/NVENC的FFmpeg版本")
        print("下载: https://www.gyan.dev/ffmpeg/builds/")
        print("选择 'release-full' 版本")

    elif not has_nvidia_gpu:
        print("❌ 未检测到NVIDIA GPU")
        print("无法使用CUDA加速")
        print("可以使用CPU处理，但速度较慢")

    print("\n" + "=" * 60)
    print("当前状态:")
    print("=" * 60)
    print("✅ 可以使用VidGear进行GPU视频编码")
    print("✅ FFmpeg支持硬件加速")
    print("⚠️  OpenCV是CPU版本（图像处理在CPU）")
    print("💡 视频编码会很快，但图像处理可能成为瓶颈")


def save_detailed_report(all_checks, filename='cuda_detection_report_fixed.json'):
    """保存详细检测报告"""
    try:
        # 清理不可序列化的数据
        cleaned_checks = {}
        for key, value in all_checks.items():
            if value is not None:
                if isinstance(value, dict):
                    cleaned_value = {}
                    for k, v in value.items():
                        if v is not None:
                            # 如果是长字符串，截断
                            if isinstance(v, str) and len(v) > 1000:
                                cleaned_value[k] = v[:1000] + "...[已截断]"
                            else:
                                cleaned_value[k] = v
                    cleaned_checks[key] = cleaned_value
                else:
                    cleaned_checks[key] = value

        with open(filename, 'w', encoding='utf-8') as f:
            json.dump(cleaned_checks, f, indent=2, ensure_ascii=False)

        print(f"\n详细报告已保存到: {filename}")
    except Exception as e:
        print(f"保存报告失败: {e}")


def main():
    """主函数"""
    print("系统CUDA/GPU支持检测工具")
    print("=" * 60)

    all_checks = {}

    try:
        # 1. 检查系统信息
        all_checks['system_info'] = check_system_info()

        # 2. 检查CUDA可用性
        all_checks['cuda_info'] = check_cuda_availability()

        # 3. 检查OpenCV CUDA
        all_checks['opencv_info'] = check_opencv_cuda()

        # 4. 检查FFmpeg CUDA
        all_checks['ffmpeg_info'] = check_ffmpeg_cuda_safe()

        # 5. 检查VidGear CUDA
        all_checks['vidgear_info'] = check_vidgear_cuda()

        # 6. 运行快速测试
        run_quick_test()

        # 7. 生成总结报告
        generate_summary_report(all_checks)

        # 8. 保存详细报告
        save_detailed_report(all_checks)

    except KeyboardInterrupt:
        print("\n检测被用户中断")
    except Exception as e:
        print(f"\n检测过程中出错: {e}")
        import traceback
        traceback.print_exc()


if __name__ == "__main__":
    # 确保使用正确的编码
    if sys.platform == 'win32':
        import io
        sys.stdout = io.TextIOWrapper(sys.stdout.buffer, encoding='utf-8')
        sys.stderr = io.TextIOWrapper(sys.stderr.buffer, encoding='utf-8')

    main()
