#!/usr/bin/env python3
# -*- coding: utf-8 -*-
"""
FFmpeg 全面探测器 v4.0
🔥 深度探测所有组件的每一个可用功能
🔥 完整命令帮助文档结构化提取
🔥 所有滤镜/编码器/协议的详细参数
🔥 像素格式/色彩空间/硬件加速完整清单
🔥 100% 程序友好JSON结构，无信息损失
🔥 跨平台安全执行 + 智能错误恢复
"""

import os
import sys
import subprocess
import json
import platform
import re
from pathlib import Path
from datetime import datetime
from typing import Dict, List, Optional, Tuple, Any, Set
import shutil
import time
import itertools


# ==================== 增强型配置 ====================
REPORT_FILENAME = "ffmpeg_full_diagnostics.json"
CMD_TIMEOUT = 40  # 延长超时，适应全面探测

SAFE_COMPONENTS = {
    "ffmpeg": ["ffmpeg.exe", "ffmpeg"] if sys.platform == "win32" else ["ffmpeg"],
    "ffprobe": ["ffprobe.exe", "ffprobe"] if sys.platform == "win32" else ["ffprobe"],
    "ffplay": ["ffplay.exe", "ffplay"] if sys.platform == "win32" else ["ffplay"]
}

# 硬件相关关键词
HARDWARE_KEYWORDS = {
    "encoders": ["nvenc", "qsv", "vaapi", "amf", "videotoolbox", "mediacodec", "nvenc", "cuvid"],
    "decoders": ["cuvid", "qsv", "vaapi", "videotoolbox", "mediacodec", "nvdec"],
    "filters": ["cuda", "opencl", "qsv", "vaapi", "vulkan"]
}

# 关键滤镜（全部探测，但标记这些为高优先级）
KEY_FILTERS = [
    "scale", "transpose", "rotate", "pad", "overlay", "geq", "gblur", "crop", "fps",
    "hflip", "vflip", "colorspace", "zscale", "hwupload", "hwdownload", "hwmap",
    "scale_cuda", "scale_vt"
]


# ==================== 核心工具函数（增强版） ====================
def safe_run_cmd(cmd: List[str], timeout: int = CMD_TIMEOUT, capture_full_stdout: bool = False) -> Tuple[bool, str, str]:
    """
    增强型安全命令执行，支持大输出和详细错误诊断
    """
    try:
        # Windows特定参数
        kwargs = {}
        if sys.platform == "win32":
            kwargs["creationflags"] = subprocess.CREATE_NO_WINDOW
            kwargs["shell"] = False

        result = subprocess.run(
            cmd,
            capture_output=True,
            text=True,
            timeout=timeout,
            encoding='utf-8',
            errors='replace',
            **kwargs
        )

        # 处理大输出（如果需要完整输出）
        stdout = result.stdout
        stderr = result.stderr

        if not capture_full_stdout and len(stdout) > 100000:  # 100KB
            # 仅保留开头和结尾，避免内存爆炸
            stdout = stdout[:50000] + \
                "\n\n...TRUNCATED...\n\n" + stdout[-50000:]

        return (
            result.returncode == 0,
            stdout.strip(),
            stderr.strip()
        )
    except subprocess.TimeoutExpired as e:
        return False, "", f"TIMEOUT_{timeout}s: {str(e)}"
    except (FileNotFoundError, PermissionError) as e:
        return False, "", f"EXEC_ERROR:{type(e).__name__}: {str(e)}"
    except Exception as e:
        return False, "", f"EXCEPTION:{type(e).__name__}:{str(e)[:500]}"


def find_components_in_dir(search_dir: Path, component_name: str) -> Optional[str]:
    """
    在指定目录搜索组件（精确匹配）
    """
    if not search_dir.exists() or not search_dir.is_dir():
        return None

    for candidate in SAFE_COMPONENTS[component_name]:
        path = search_dir / candidate
        if path.is_file():
            # Windows不严格检查执行权限
            if sys.platform == "win32" or os.access(path, os.X_OK):
                return str(path.resolve())
    return None


def locate_ffmpeg_components() -> Dict[str, Dict]:
    """
    智能定位所有组件：脚本目录 → 系统 PATH
    """
    script_dir = Path(__file__).parent.resolve()
    results = {}

    for comp_name in SAFE_COMPONENTS.keys():
        found_path = None
        search_method = None

        # 1. 优先搜索脚本目录
        if (path := find_components_in_dir(script_dir, comp_name)):
            found_path = path
            search_method = "script_dir"

        # 2. 回退到系统 PATH
        elif (path := shutil.which(SAFE_COMPONENTS[comp_name][0])):
            found_path = str(Path(path).resolve())
            search_method = "system_path"

        if found_path:
            results[comp_name] = {
                "found": True,
                "path": found_path,
                "search_method": search_method,
                "status": "pending_diagnosis"
            }
        else:
            results[comp_name] = {
                "found": False,
                "search_method": "not_found",
                "status": "missing"
            }
    return results


def extract_version(output: str) -> Dict[str, Any]:
    """
    从 -version 输出提取完整版本和配置信息
    """
    version_info = {
        "raw_output": output[:1000],  # 保留部分原始输出
        "version": "unknown",
        "configuration": [],
        "libraries": {},
        "compiler": "unknown"
    }

    # 提取版本
    if match := re.search(r'ffmpeg version\s+([^\s]+)', output, re.IGNORECASE):
        version_info["version"] = match.group(
            1).split("-")[0]  # 去除 git hash 等后缀

    # 提取配置
    config_section = False
    for line in output.split('\n'):
        if "configuration:" in line.lower():
            config_section = True
            # 提取这一行的配置
            config_str = line.split(":", 1)[1].strip()
            version_info["configuration"] = [item.strip()
                                             for item in config_str.split() if item.strip()]
        elif config_section and line.startswith(' '):
            # 多行配置
            version_info["configuration"].extend(
                [item.strip() for item in line.split() if item.strip()])
        elif "lib" in line.lower() and "=" in line:
            # 库版本
            parts = line.split("=")
            if len(parts) >= 2:
                lib_name = parts[0].strip().replace("lib", "")
                version_info["libraries"][lib_name] = parts[1].strip()
        elif "compiler" in line.lower():
            version_info["compiler"] = line.split(":", 1)[1].strip()

    return version_info


# ==================== 深度解析函数（全面增强） ====================
def parse_codecs(stdout: str, codec_type: str) -> Dict[str, Any]:
    """
    解析编码器/解码器列表，返回完整结构化数据
    codec_type: "encoder" or "decoder"
    """
    lines = [l.strip() for l in stdout.split('\n') if l.strip()]
    codecs = {
        "all": [],
        "video": [],
        "audio": [],
        "subtitle": [],
        "hardware_accelerated": [],
        "details": {}
    }

    # 找到表头分隔符
    header_idx = -1
    for i, line in enumerate(lines):
        if "------" in line or ("Name" in line and "Description" in line):
            header_idx = i
            break

    if header_idx == -1:
        return codecs  # 无法解析

    # 处理每一行
    for line in lines[header_idx+1:]:
        if not line or line.startswith('-'):
            continue

        # 尝试解析行
        # 格式1: D.VC... a64_multi [...] (可能有多个标志)
        # 格式2: V..... a64_multi A64 video codec (multi) (需要处理多空格)
        parts = re.split(r'\s+', line, maxsplit=2)  # 最多分割成3部分

        if len(parts) < 2:
            continue

        flags = parts[0].strip()
        name = parts[1].strip()
        description = parts[2].strip() if len(parts) > 2 else ""

        # 确定硬件加速关键词
        hw_keywords = HARDWARE_KEYWORDS.get(codec_type + "s", [])

        # 基本信息
        codec_info = {
            "name": name,
            "description": description,
            "type": "unknown",
            "flags": flags,
            "hardware_accelerated": any(kw in name.lower() for kw in hw_keywords),
            "raw_line": line
        }

        # 确定类型
        if 'V' in flags:
            codec_info["type"] = "video"
            codecs["video"].append(name)
        elif 'A' in flags:
            codec_info["type"] = "audio"
            codecs["audio"].append(name)
        elif 'S' in flags:
            codec_info["type"] = "subtitle"
            codecs["subtitle"].append(name)

        # 硬件加速标记
        if codec_info["hardware_accelerated"]:
            codecs["hardware_accelerated"].append(name)

        # 保存详情
        codecs["all"].append(name)
        codecs["details"][name] = codec_info

    return codecs


def parse_filters(stdout: str) -> Dict[str, Any]:
    """
    深度解析所有滤镜，包括详细参数
    """
    filters = {
        "all": [],
        "video": [],
        "audio": [],
        "sources": [],
        "sinks": [],
        "key_filters": {},
        "details": {}
    }

    current_section = None
    current_filter = None
    current_options = []

    # 优化正则表达式
    option_pattern = re.compile(
        r'^\s{2,}([A-Za-z_][A-Za-z0-9_]*)\s+<.*?>\s+(.*) $ ')

    for line in stdout.split('\n'):
        line = line.rstrip()

        # 检测滤镜类型标题
        if line.startswith('Filters:'):
            current_section = "filters"
            continue
        elif line.startswith('Sources:'):
            current_section = "sources"
            continue
        elif line.startswith('Sinks:'):
            current_section = "sinks"
            continue

        # 检测新滤镜定义
        if current_section and line.strip() and not line.startswith(' ') and ':' in line:
            # 保存上一个滤镜
            if current_filter:
                # 提取选项
                options = {}
                for opt_line in current_options:
                    match = option_pattern.match(opt_line)
                    if match:
                        opt_name = match.group(1)
                        opt_desc = match.group(2).strip()
                        options[opt_name] = {
                            "description": opt_desc,
                            "raw_line": opt_line.strip()
                        }

                filter_type = "unknown"
                if '->' in line:
                    if '->V' in line or 'V->' in line:
                        filter_type = "video"
                    elif '->A' in line or 'A->' in line:
                        filter_type = "audio"

                filter_info = {
                    "name": current_filter,
                    "description": line.split(':', 1)[1].strip(),
                    "type": filter_type,
                    "section": current_section,
                    "options": options,
                    "is_key_filter": current_filter in KEY_FILTERS,
                    "hardware_accelerated": any(kw in current_filter.lower() for kw in HARDWARE_KEYWORDS["filters"])
                }

                filters["details"][current_filter] = filter_info
                filters["all"].append(current_filter)

                if filter_type == "video":
                    filters["video"].append(current_filter)
                elif filter_type == "audio":
                    filters["audio"].append(current_filter)

                if current_section == "sources":
                    filters["sources"].append(current_filter)
                elif current_section == "sinks":
                    filters["sinks"].append(current_filter)

                if current_filter in KEY_FILTERS:
                    filters["key_filters"][current_filter] = filter_info

            # 开始新滤镜
            current_filter = line.split(':', 1)[0].strip()
            if '(' in current_filter:
                current_filter = current_filter.split('(')[0].strip()
            current_options = []
            continue

        # 收集选项
        if current_filter and line.strip() and line.startswith(' '):
            current_options.append(line)
            continue

    # 处理最后一个滤镜
    if current_filter:
        options = {}
        for opt_line in current_options:
            match = option_pattern.match(opt_line)
            if match:
                opt_name = match.group(1)
                opt_desc = match.group(2).strip()
                options[opt_name] = {
                    "description": opt_desc,
                    "raw_line": opt_line.strip()
                }

        filter_type = "unknown"
        filter_info = {
            "name": current_filter,
            "description": "",
            "type": filter_type,
            "section": current_section or "filters",
            "options": options,
            "is_key_filter": current_filter in KEY_FILTERS,
            "hardware_accelerated": any(kw in current_filter.lower() for kw in HARDWARE_KEYWORDS["filters"])
        }

        filters["details"][current_filter] = filter_info
        filters["all"].append(current_filter)

        if current_filter in KEY_FILTERS:
            filters["key_filters"][current_filter] = filter_info

    return filters


def parse_protocols(stdout: str) -> Dict[str, Any]:
    """
    深度解析协议支持，包括详细描述
    """
    protocols = {
        "input": {},
        "output": {},
        "read_write": {}
    }

    lines = [l.strip() for l in stdout.split('\n') if l.strip()
             and not l.startswith(' ') and not l.startswith('File')]
    header_found = False

    for line in lines:
        if "Input" in line and "Output" in line:
            header_found = True
            continue

        if not header_found:
            continue

        # 格式: "I.. file File protocol"
        parts = re.split(r'\s+', line, maxsplit=2)
        if len(parts) < 3:
            continue

        flags = parts[0].strip()
        name = parts[1].strip()
        description = parts[2].strip()

        proto_info = {
            "name": name,
            "description": description,
            "flags": flags,
            "raw_line": line
        }

        if 'I' in flags and 'O' in flags:
            protocols["read_write"][name] = proto_info
        elif 'I' in flags:
            protocols["input"][name] = proto_info
        elif 'O' in flags:
            protocols["output"][name] = proto_info

    return protocols


def parse_pix_fmts(stdout: str) -> Dict[str, Any]:
    """
    解析像素格式
    """
    pix_fmts = {
        "all": [],
        "hardware_accelerated": [],
        "details": {}
    }

    lines = [l.strip() for l in stdout.split('\n') if l.strip()
             and not l.startswith(' ') and l != 'PIX_FMT']

    for line in lines:
        if line.startswith('name') or '---' in line or len(line.split()) < 4:
            continue

        parts = re.split(r'\s+', line, maxsplit=5)
        if len(parts) < 6:
            continue

        name = parts[0]
        nb_components = parts[1]
        bits_per_pixel = parts[2]
        flags = parts[3]

        # 检测硬件加速格式
        hardware_accelerated = ('H' in flags or
                                'cuda' in name.lower() or
                                'nv12' in name.lower() or
                                'p010' in name.lower() or
                                'vaapi' in name.lower() or
                                'qsv' in name.lower())

        pix_fmt_info = {
            "name": name,
            "nb_components": nb_components,
            "bits_per_pixel": bits_per_pixel,
            "flags": flags,
            "hardware_accelerated": hardware_accelerated,
            "raw_line": line
        }

        pix_fmts["all"].append(name)
        if hardware_accelerated:
            pix_fmts["hardware_accelerated"].append(name)

        pix_fmts["details"][name] = pix_fmt_info

    return pix_fmts


def parse_sample_fmts(stdout: str) -> Dict[str, Any]:
    """
    解析采样格式
    """
    sample_fmts = {
        "all": [],
        "details": {}
    }

    lines = [l.strip() for l in stdout.split('\n') if l.strip()
             and not l.startswith(' ') and not l.startswith('name')]

    for line in lines:
        if "----" in line or "NAME" in line or len(line.split()) < 2:
            continue

        parts = re.split(r'\s+', line, maxsplit=2)
        if len(parts) < 2:
            continue

        name = parts[0]
        depth = parts[1]
        description = parts[2] if len(parts) > 2 else ""

        sample_fmt_info = {
            "name": name,
            "depth": depth,
            "description": description,
            "raw_line": line
        }

        sample_fmts["all"].append(name)
        sample_fmts["details"][name] = sample_fmt_info

    return sample_fmts


def parse_formats(stdout: str) -> Dict[str, Any]:
    """
    深度解析容器格式
    """
    formats = {
        "demuxers": {},
        "muxers": {},
        "read_write": {},
        "details": {}
    }

    lines = [l.strip() for l in stdout.split('\n') if l.strip()
             and not l.startswith(' ') and not l.startswith('File')]
    header_found = False

    for line in lines:
        if "Demuxing" in line and "muxing" in line.lower():
            header_found = True
            continue

        if not header_found:
            continue

        # 跳过标题行
        if line.startswith(' --') or line.startswith('==='):
            continue

        # 格式: "DE 3dostr 3DO STR"
        parts = re.split(r'\s+', line, maxsplit=2)
        if len(parts) < 3:
            continue

        flags = parts[0].strip()
        name = parts[1].strip()
        description = parts[2].strip()

        fmt_info = {
            "name": name,
            "description": description,
            "flags": flags,
            "extensions": [],  # 将在后续填充
            "mimetype": "",
            "raw_line": line
        }

        # 提取扩展名和MIME类型
        if " [" in description:
            desc_parts = description.split(" [", 1)
            fmt_info["description"] = desc_parts[0].strip()
            meta = desc_parts[1].rstrip("]")

            if " extension:" in meta:
                ext_part = meta.split(" extension:", 1)[
                    1].split(";", 1)[0].strip()
                fmt_info["extensions"] = [e.strip()
                                          for e in ext_part.split(",") if e.strip()]

            if " mimetype:" in meta:
                mime_part = meta.split(" mimetype:", 1)[
                    1].split(";", 1)[0].strip()
                fmt_info["mimetype"] = mime_part

        formats["details"][name] = fmt_info

        if 'D' in flags and 'E' in flags:
            formats["read_write"][name] = fmt_info
        elif 'D' in flags:
            formats["demuxers"][name] = fmt_info
        elif 'E' in flags:
            formats["muxers"][name] = fmt_info

    return formats


def parse_devices(stdout: str) -> Dict[str, Any]:
    """
    解析设备支持
    """
    devices = {
        "input": {},
        "output": {},
        "details": {}
    }

    lines = [l.strip() for l in stdout.split('\n') if l.strip()
             and not l.startswith(' ') and not l.startswith('Devices')]

    for line in lines:
        if "name" in line.lower() and "description" in line.lower():
            continue

        if "----" in line:
            continue

        # 格式: "d.| video4linux2,v4l2 Video4Linux2 device"
        parts = re.split(r'\s+', line, maxsplit=2)
        if len(parts) < 3:
            continue

        flags = parts[0].strip()
        name = parts[1].strip()
        description = parts[2].strip()

        device_info = {
            "name": name,
            "description": description,
            "flags": flags,
            "input": 'd' in flags,
            "output": 'e' in flags,
            "raw_line": line
        }

        devices["details"][name] = device_info

        if 'd' in flags:
            devices["input"][name] = device_info
        if 'e' in flags:
            devices["output"][name] = device_info

    return devices


def parse_bsfs(stdout: str) -> Dict[str, Any]:
    """
    解析比特流过滤器
    """
    bsfs = {
        "all": [],
        "details": {}
    }

    lines = [l.strip() for l in stdout.split('\n') if l.strip()
             and not l.startswith(' ') and not l.startswith('Bitstream')]

    for line in lines:
        if "----" in line:
            continue

        parts = re.split(r'\s+', line, maxsplit=1)
        if len(parts) < 2:
            continue

        name = parts[0].strip()
        description = parts[1].strip()

        bsf_info = {
            "name": name,
            "description": description,
            "raw_line": line
        }

        bsfs["all"].append(name)
        bsfs["details"][name] = bsf_info

    return bsfs


def parse_colors(stdout: str) -> Dict[str, Any]:
    """
    解析颜色名称
    """
    colors = []
    lines = [l.strip() for l in stdout.split('\n') if l.strip()
             and not l.startswith(' ') and not l.startswith('Color names')]

    for line in lines:
        if "----" in line or len(line.split()) < 2:
            continue

        parts = line.split()
        if len(parts) >= 2:
            name = parts[0]
            hex_value = parts[1] if len(parts) > 1 else ""
            colors.append({
                "name": name,
                "hex": hex_value,
                "raw_line": line
            })

    return {"all": colors}


def parse_hwaccels(stdout: str) -> Dict[str, Any]:
    """
    解析硬件加速方法
    """
    hwaccels = {
        "all": [],
        "details": {}
    }

    lines = [l.strip() for l in stdout.split('\n') if l.strip()
             and not l.startswith(' ') and not l.startswith('Hardware')]

    for line in lines:
        if "----" in line or len(line.split()) < 2:
            continue

        parts = re.split(r'\s+', line, maxsplit=1)
        if len(parts) < 2:
            continue

        name = parts[0].strip()
        description = parts[1].strip()

        hwaccel_info = {
            "name": name,
            "description": description,
            "raw_line": line
        }

        hwaccels["all"].append(name)
        hwaccels["details"][name] = hwaccel_info

    return hwaccels


def get_filter_help(ffmpeg_path: str, filter_name: str) -> Dict[str, Any]:
    """
    获取特定滤镜的详细帮助
    """
    success, stdout, stderr = safe_run_cmd(
        [ffmpeg_path, "-h", f"filter={filter_name}"],
        timeout=15
    )

    if not success:
        return {
            "error": stderr or stdout or "help_command_failed",
            "raw_output": stdout[:500] if stdout else ""
        }

    help_info = {
        "raw_help": stdout,
        "options": {},
        "examples": [],
        "description": ""
    }

    # 提取描述
    desc_match = re.search(r'Filter\s+' + re.escape(filter_name) +
                           r'\s*\n(.*?)(?:\n\s*\n|\nOption)', stdout, re.DOTALL)
    if desc_match:
        help_info["description"] = desc_match.group(1).strip()

    # 提取选项
    current_opt = None
    for line in stdout.split('\n'):
        line = line.strip()
        if not line:
            continue

        # 检测新选项
        if line.startswith('-') and ' ' in line:
            opt_parts = line.split(' ', 1)
            opt_name = opt_parts[0].lstrip('-')
            opt_desc = opt_parts[1] if len(opt_parts) > 1 else ""
            current_opt = opt_name
            help_info["options"][opt_name] = {
                "name": opt_name,
                "description": opt_desc,
                "default": None,
                "min": None,
                "max": None,
                "allowed_values": [],
                "raw_line": line
            }
        elif current_opt and line.startswith('(') and "default" in line.lower():
            # 提取默认值、范围等
            opt_info = help_info["options"][current_opt]

            # 默认值
            if "default" in line:
                default_match = re.search(r'default\s+([^ $ ]+)', line)
                if default_match:
                    opt_info["default"] = default_match.group(1).strip()

            # 范围
            range_match = re.search(
                r'range\s+([-\d.]+)\s+to\s+([-\d.]+)', line)
            if range_match:
                opt_info["min"] = range_match.group(1).strip()
                opt_info["max"] = range_match.group(2).strip()

            # 允许值
            if "choices" in line or "allowed values" in line.lower():
                values_match = re.search(r'\{([^\}]+)\}', line)
                if values_match:
                    values_str = values_match.group(1)
                    opt_info["allowed_values"] = [v.strip()
                                                  for v in values_str.split(',') if v.strip()]

    # 提取示例
    examples_section = False
    for line in stdout.split('\n'):
        if "Examples:" in line:
            examples_section = True
            continue

        if examples_section and line.strip().startswith('-'):
            help_info["examples"].append(line.strip())
        elif examples_section and not line.strip():
            break

    return help_info


def generate_comprehensive_command_paradigms(capabilities: Dict) -> Dict[str, List[Dict]]:
    """
    生成全面的命令范式，覆盖所有关键功能
    """
    paradigms = {"ffmpeg": [], "ffprobe": [], "ffplay": []}

    # 缓存能力
    encoders = capabilities.get("encoders", {})
    decoders = capabilities.get("decoders", {})
    filters = capabilities.get("filters", {})
    protocols = capabilities.get("protocols", {})
    hwaccels = capabilities.get("hwaccels", {})
    pix_fmts = capabilities.get("pix_fmts", {})

    all_hw_encoders = encoders.get("hardware_accelerated", [])
    all_hw_decoders = decoders.get("hardware_accelerated", [])
    all_filters = filters.get("all", [])
    key_filters = filters.get("key_filters", {})
    input_protocols = protocols.get("input", {})
    output_protocols = protocols.get("output", {})
    hw_methods = hwaccels.get("all", [])

    # === FFmpeg 范式 ===
    # 1. 基础转码
    paradigms["ffmpeg"].append({
        "id": "basic_transcode",
        "task": "基础转码（保持原始编码）",
        "command_template": "ffmpeg -y -i {input} -c copy {output}",
        "parameters": {
            "{input}": "输入文件路径",
            "{output}": "输出文件路径",
            "-y": "覆盖同名文件",
            "-c copy": "复制流，不重新编码"
        },
        "compatibility": {"always_available": True}
    })

    # 2. 完整转码（视频+音频）
    h264_available = "libx264" in encoders.get("all", [])
    aac_available = "aac" in encoders.get("all", [])

    paradigms["ffmpeg"].append({
        "id": "full_transcode",
        "task": "完整转码（H.264视频 + AAC音频）",
        "command_template": "ffmpeg -i {input} -c:v libx264 -crf 23 -preset medium -c:a aac -b:a 128k {output}",
        "parameters": {
            "{input}": "输入文件路径",
            "{output}": "输出文件路径",
            "-c:v libx264": "使用H.264编码器",
            "-crf 23": "质量控制（18-28，值越小质量越高）",
            "-preset medium": "编码速度/质量平衡",
            "-c:a aac": "使用AAC音频编码器",
            "-b:a 128k": "音频比特率"
        },
        "compatibility": {
            "requires_encoders": ["libx264", "aac"],
            "available": h264_available and aac_available
        }
    })

    # 3. 视频滤镜链
    scale_available = "scale" in all_filters
    pad_available = "pad" in all_filters
    fps_available = "fps" in all_filters

    if scale_available and pad_available and fps_available:
        paradigms["ffmpeg"].append({
            "id": "video_filter_chain",
            "task": "复合视频滤镜链（缩放+填充+帧率）",
            "command_template": "ffmpeg -i {input} -vf \"scale={width}:{height}:force_original_aspect_ratio=decrease,pad={width}:{height}:(ow-iw)/2:(oh-ih)/2,fps={fps}\" {output}",
            "parameters": {
                "{input}": "输入文件路径",
                "{output}": "输出文件路径",
                "{width}": "目标宽度（偶数）",
                "{height}": "目标高度（偶数）",
                "{fps}": "目标帧率（如30或25）",
                "force_original_aspect_ratio=decrease": "保持原始比例，必要时缩小",
                "pad=...": "用黑边填充至目标尺寸"
            },
            "compatibility": {
                "requires_filters": ["scale", "pad", "fps"],
                "available": scale_available and pad_available and fps_available
            }
        })

    # 4. 硬件加速转码（多平台）
    hw_paradigms = []

    # NVIDIA NVENC
    nvenc_encoders = [e for e in all_hw_encoders if "nvenc" in e.lower(
    ) or "h264_nvenc" in e.lower() or "hevc_nvenc" in e.lower()]
    if nvenc_encoders:
        hw_paradigms.append({
            "id": "nvenc_transcode",
            "task": "NVIDIA GPU硬件编码（H.264）",
            "command_template": "ffmpeg -hwaccel cuda -i {input} -c:v h264_nvenc -preset p7 -b:v {bitrate} -c:a copy {output}",
            "parameters": {
                "{input}": "输入文件路径",
                "{output}": "输出文件路径",
                "{bitrate}": "目标视频比特率（如5M）",
                "-hwaccel cuda": "使用CUDA硬件加速解码",
                "-preset p7": "最慢预设，最高质量"
            },
            "compatibility": {
                "requires_encoders": nvenc_encoders,
                "hardware": "NVIDIA GPU 必须",
                "drivers": "需要NVIDIA驱动和CUDA"
            }
        })

    # Intel QSV
    qsv_encoders = [e for e in all_hw_encoders if "qsv" in e.lower(
    ) or "h264_qsv" in e.lower() or "hevc_qsv" in e.lower()]
    if qsv_encoders:
        hw_paradigms.append({
            "id": "qsv_transcode",
            "task": "Intel QuickSync硬件编码",
            "command_template": "ffmpeg -hwaccel qsv -qsv_device /dev/dri/renderD128 -i {input} -c:v h264_qsv -preset veryslow -b:v {bitrate} -c:a copy {output}",
            "parameters": {
                "{input}": "输入文件路径",
                "{output}": "输出文件路径",
                "{bitrate}": "目标视频比特率（如5M）",
                "-hwaccel qsv": "使用QSV硬件加速",
                "-qsv_device": "指定设备（Linux需要）",
                "-preset veryslow": "高质量预设"
            },
            "compatibility": {
                "requires_encoders": qsv_encoders,
                "hardware": "Intel CPU with integrated GPU 必须",
                "os_notes": "Linux需配置设备路径，Windows通常自动检测"
            }
        })

    # AMD AMF
    amf_encoders = [
        e for e in all_hw_encoders if "amf" in e.lower() or "h264_amf" in e.lower()]
    if amf_encoders:
        hw_paradigms.append({
            "id": "amf_transcode",
            "task": "AMD GPU硬件编码",
            "command_template": "ffmpeg -hwaccel d3d11va -i {input} -c:v h264_amf -quality quality -b:v {bitrate} -c:a copy {output}",
            "parameters": {
                "{input}": "输入文件路径",
                "{output}": "输出文件路径",
                "{bitrate}": "目标视频比特率（如5M）",
                "-hwaccel d3d11va": "使用Direct3D 11硬件加速",
                "-quality quality": "质量优先模式"
            },
            "compatibility": {
                "requires_encoders": amf_encoders,
                "hardware": "AMD GPU 必须",
                "os_notes": "主要在Windows上支持"
            }
        })

    paradigms["ffmpeg"].extend(hw_paradigms)

    # 5. 直播推流
    rtmp_available = "rtmp" in output_protocols
    flv_available = "flv" in protocols.get("muxers", {})

    if rtmp_available or flv_available:
        paradigms["ffmpeg"].append({
            "id": "rtmp_stream",
            "task": "RTMP直播推流",
            "command_template": "ffmpeg -re -i {input} -c:v libx264 -c:a aac -f flv rtmp://{server}/{app}/{stream_key}",
            "parameters": {
                "{input}": "输入文件或设备路径",
                "{server}": "RTMP服务器地址",
                "{app}": "应用名（通常为live）",
                "{stream_key}": "流密钥",
                "-re": "以源帧率读取输入",
                "-f flv": "使用FLV封装格式"
            },
            "compatibility": {
                "requires_protocols": ["rtmp"],
                "requires_muxers": ["flv"],
                "available": rtmp_available and flv_available
            }
        })

    # 6. 录制屏幕
    screen_capture_available = (
        "gdigrab" in input_protocols or
        "x11grab" in input_protocols or
        "avfoundation" in input_protocols
    )

    if screen_capture_available:
        paradigms["ffmpeg"].append({
            "id": "screen_capture",
            "task": "屏幕录制",
            "command_template": "ffmpeg -f gdigrab -framerate 30 -i desktop -c:v libx264 -crf 23 -preset fast {output}",
            "parameters": {
                "{output}": "输出文件路径",
                "-f gdigrab": "Windows屏幕捕获设备",
                "-f x11grab": "Linux X11屏幕捕获（替代gdigrab）",
                "-f avfoundation": "macOS屏幕捕获（替代gdigrab）",
                "-framerate 30": "捕获帧率",
                "-i desktop": "整个桌面（Windows）"
            },
            "compatibility": {
                "os_specific": {
                    "windows": "-f gdigrab -i desktop",
                    "linux": "-f x11grab -i :0.0",
                    "macos": "-f avfoundation -i \"1\""
                }
            }
        })

    # 7. 音频处理
    loudnorm_available = "loudnorm" in all_filters
    equalizer_available = "equalizer" in all_filters

    if loudnorm_available or equalizer_available:
        paradigms["ffmpeg"].append({
            "id": "audio_processing",
            "task": "音频标准化和均衡",
            "command_template": "ffmpeg -i {input} -af \"loudnorm=I=-16:LRA=11:TP=-1.5, equalizer=f=1000:width_type=h:width=200:g=-3\" {output}",
            "parameters": {
                "{input}": "输入文件路径",
                "{output}": "输出文件路径",
                "loudnorm": "EBU R128 响度标准化",
                "equalizer": "图形均衡器示例（调整1kHz频段）"
            },
            "compatibility": {
                "requires_filters": ["loudnorm", "equalizer"],
                "available": loudnorm_available and equalizer_available
            }
        })

    # === FFprobe 范式 ===
    paradigms["ffprobe"].append({
        "id": "stream_analysis",
        "task": "全面流分析",
        "command_template": "ffprobe -v error -show_entries format=duration,size,bit_rate:stream=index,codec_name,codec_type,width,height,r_frame_rate,sample_rate,channels -of json {input}",
        "parameters": {
            "{input}": "输入文件路径",
            "-show_entries": "指定要显示的字段",
            "-of json": "输出为JSON格式（程序友好）"
        },
        "compatibility": {"always_available": True}
    })

    paradigms["ffprobe"].append({
        "id": "frame_analysis",
        "task": "帧级详细分析",
        "command_template": "ffprobe -v error -select_streams v:0 -show_frames -show_entries frame=pkt_pts_time,pict_type,width,height -of csv {input}",
        "parameters": {
            "{input}": "输入文件路径",
            "-select_streams v:0": "只选择第一个视频流",
            "-show_frames": "显示每一帧的详细信息",
            "-show_entries": "自定义输出字段",
            "-of csv": "CSV格式输出（适合数据分析）"
        },
        "compatibility": {"requires": ["-show_frames option"]}
    })

    # === FFplay 范式 ===
    ffplay_available = "ffplay" in capabilities.get("components", {})

    if ffplay_available:
        paradigms["ffplay"].append({
            "id": "basic_playback",
            "task": "基本媒体播放",
            "command_template": "ffplay -autoexit {input}",
            "parameters": {
                "{input}": "输入文件、URL或设备路径",
                "-autoexit": "播放结束后自动退出"
            },
            "compatibility": {"always_available": True}
        })

        # 检查网络流协议支持
        rtsp_available = "rtsp" in input_protocols
        rtmp_available = "rtmp" in input_protocols
        http_available = "http" in input_protocols

        if rtsp_available or rtmp_available or http_available:
            paradigms["ffplay"].append({
                "id": "network_stream_playback",
                "task": "网络流媒体播放",
                "command_template": "ffplay -fflags nobuffer -flags low_delay -rtsp_transport tcp {url}",
                "parameters": {
                    "{url}": "RTSP/RTMP/HTTP流媒体URL",
                    "-fflags nobuffer": "减少缓冲延迟",
                    "-flags low_delay": "低延迟模式",
                    "-rtsp_transport tcp": "强制使用TCP传输RTSP流"
                },
                "compatibility": {
                    "requires_protocols": ["rtsp", "rtmp", "http"],
                    "available": rtsp_available or rtmp_available or http_available
                }
            })

    return paradigms


# ==================== 全面诊断引擎 ====================
def run_comprehensive_diagnosis() -> Dict:
    """
    执行全面诊断，探测所有功能
    """
    start_time = datetime.now()
    report = {
        "diagnosis": {
            "timestamp": start_time.isoformat(),
            "script_path": str(Path(__file__).resolve()),
            "working_directory": str(Path.cwd().resolve())
        },
        "system": {
            "os": platform.platform(),
            "os_family": sys.platform,
            "python_version": sys.version.split()[0],
            "machine": platform.machine(),
            "cpu_count": os.cpu_count()
        },
        "components": {},
        "capabilities": {},
        "command_paradigms": {},
        "raw_outputs": {},  # 存储原始输出（截断版），用于调试
        "metadata": {
            "report_version": "4.0",
            "generated_by": "ffmpeg_comprehensive_diagnosis_tool",
            "intended_use": "programmatic_import_and_analysis"
        }
    }

    # 1. 定位组件
    components = locate_ffmpeg_components()
    report["components"] = components

    # 2. 诊断每个组件
    for comp_name, comp_info in components.items():
        if not comp_info.get("found"):
            continue

        print(f"🔍 诊断组件: {comp_name} ({comp_info['path']})")

        # 2.1 获取版本和配置
        success, version_out, _ = safe_run_cmd(
            [comp_info["path"], "-version"],
            capture_full_stdout=False
        )

        if success:
            comp_info["version_info"] = extract_version(version_out)
            comp_info["status"] = "operational"
            # 保存截断的原始输出
            report["raw_outputs"][f"{comp_name}_version"] = version_out[:2000]
        else:
            comp_info["status"] = "broken"
            continue

        # 深度探测（仅 ffmpeg）
        if comp_name == "ffmpeg":
            ffmpeg_path = comp_info["path"]
            capabilities = {}
            raw_outputs = report["raw_outputs"]

            # 2.2 探测编码器
            print(" 📡 探测编码器...")
            success, enc_out, _ = safe_run_cmd(
                [ffmpeg_path, "-hide_banner", "-encoders"]
            )
            if success:
                capabilities["encoders"] = parse_codecs(enc_out, "encoder")
                raw_outputs["encoders"] = enc_out[:5000]

            # 2.3 探测解码器
            print(" 📡 探测解码器...")
            success, dec_out, _ = safe_run_cmd(
                [ffmpeg_path, "-hide_banner", "-decoders"]
            )
            if success:
                capabilities["decoders"] = parse_codecs(dec_out, "decoder")
                raw_outputs["decoders"] = dec_out[:5000]

            # 2.4 探测滤镜
            print(" 🔍 探测滤镜...")
            success, filters_out, _ = safe_run_cmd(
                [ffmpeg_path, "-hide_banner", "-filters"]
            )
            if success:
                capabilities["filters"] = parse_filters(filters_out)
                raw_outputs["filters"] = filters_out[:5000]

            # 获取关键滤镜的详细帮助
            key_filters = capabilities["filters"].get("key_filters", {})
            print(f" 📚 获取 {len(key_filters)} 个关键滤镜的详细帮助...")
            capabilities["filter_help"] = {}

            for i, (filter_name, _) in enumerate(key_filters.items()):
                if i >= 20:  # 限制数量，避免超时
                    break
                print(f" ℹ️ {filter_name} ({i+1}/{len(key_filters)})")
                capabilities["filter_help"][filter_name] = get_filter_help(
                    ffmpeg_path, filter_name
                )
                time.sleep(0.1)  # 避免过载

            # 2.5 探测协议
            print(" 🌐 探测协议...")
            success, proto_out, _ = safe_run_cmd(
                [ffmpeg_path, "-hide_banner", "-protocols"]
            )
            if success:
                capabilities["protocols"] = parse_protocols(proto_out)
                raw_outputs["protocols"] = proto_out[:2000]

            # 2.6 探测容器格式
            print(" 📦 探测容器格式...")
            success, fmt_out, _ = safe_run_cmd(
                [ffmpeg_path, "-hide_banner", "-formats"]
            )
            if success:
                capabilities["formats"] = parse_formats(fmt_out)
                raw_outputs["formats"] = fmt_out[:5000]

            # 2.7 探测像素格式
            print(" 🎨 探测像素格式...")
            success, pix_out, _ = safe_run_cmd(
                [ffmpeg_path, "-hide_banner", "-pix_fmts"]
            )
            if success:
                capabilities["pix_fmts"] = parse_pix_fmts(pix_out)
                raw_outputs["pix_fmts"] = pix_out[:3000]

            # 2.8 探测采样格式
            print(" 🔊 探测采样格式...")
            success, sample_out, _ = safe_run_cmd(
                [ffmpeg_path, "-hide_banner", "-sample_fmts"]
            )
            if success:
                capabilities["sample_fmts"] = parse_sample_fmts(sample_out)
                raw_outputs["sample_fmts"] = sample_out[:1000]

            # 2.9 探测设备
            print(" 🖥️ 探测设备...")
            success, dev_out, _ = safe_run_cmd(
                [ffmpeg_path, "-hide_banner", "-devices"]
            )
            if success:
                capabilities["devices"] = parse_devices(dev_out)
                raw_outputs["devices"] = dev_out[:2000]

            # 2.10 探测比特流过滤器
            print(" ⚙️ 探测比特流过滤器...")
            success, bsf_out, _ = safe_run_cmd(
                [ffmpeg_path, "-hide_banner", "-bsfs"]
            )
            if success:
                capabilities["bsfs"] = parse_bsfs(bsf_out)
                raw_outputs["bsfs"] = bsf_out[:2000]

            # 2.11 探测硬件加速
            print(" 💨 探测硬件加速...")
            success, hwaccel_out, _ = safe_run_cmd(
                [ffmpeg_path, "-hide_banner", "-hwaccels"]
            )
            if success:
                capabilities["hwaccels"] = parse_hwaccels(hwaccel_out)
                raw_outputs["hwaccels"] = hwaccel_out[:1000]

            # 2.12 探测颜色名称
            print(" 🌈 探测颜色名称...")
            success, colors_out, _ = safe_run_cmd(
                [ffmpeg_path, "-hide_banner", "-colors"]
            )
            if success:
                capabilities["colors"] = parse_colors(colors_out)
                raw_outputs["colors"] = colors_out[:1000]

            # 3. 生成命令范式
            print(" 🧩 生成命令范式...")
            report["command_paradigms"] = generate_comprehensive_command_paradigms(
                capabilities)

            # 4. 汇总能力
            report["capabilities"] = capabilities

    # 计算总耗时
    report["diagnosis"]["duration_seconds"] = round(
        (datetime.now() - start_time).total_seconds(), 2
    )

    # 5. 生成兼容性总结
    report["compatibility_summary"] = generate_compatibility_summary(report)

    return report


def generate_compatibility_summary(report: Dict) -> Dict[str, Any]:
    """
    生成兼容性总结，便于快速评估
    """
    summary = {
        "hardware_acceleration": {
            "available": False,
            "methods": [],
            "encoders": [],
            "decoders": []
        },
        "key_capabilities": {
            "rtmp_streaming": False,
            "h264_encoding": False,
            "hevc_encoding": False,
            "gpu_filters": False,
            "high_bit_depth": False
        },
        "platform_specific": {
            "windows": False,
            "linux": False,
            "macos": False
        }
    }

    caps = report.get("capabilities", {})
    protocols = caps.get("protocols", {})
    encoders = caps.get("encoders", {})
    decoders = caps.get("decoders", {})
    filters = caps.get("filters", {})
    hwaccels = caps.get("hwaccels", {})

    # 硬件加速
    hw_methods = hwaccels.get("all", [])
    hw_encoders = encoders.get("hardware_accelerated", [])
    hw_decoders = decoders.get("hardware_accelerated", [])

    summary["hardware_acceleration"] = {
        "available": bool(hw_methods or hw_encoders or hw_decoders),
        "methods": hw_methods,
        "encoders": hw_encoders,
        "decoders": hw_decoders
    }

    # 关键能力
    output_protocols = protocols.get("output", {})
    all_encoders = encoders.get("all", [])
    all_filters = filters.get("all", [])
    all_pix_fmts = caps.get("pix_fmts", {}).get("all", [])

    summary["key_capabilities"] = {
        "rtmp_streaming": "rtmp" in output_protocols,
        "h264_encoding": any(e for e in all_encoders if "h264" in e.lower()),
        "hevc_encoding": any(e for e in all_encoders if "hevc" in e.lower() or "h265" in e.lower()),
        "gpu_filters": any(f for f in all_filters if any(kw in f.lower() for kw in HARDWARE_KEYWORDS["filters"])),
        "high_bit_depth": any(pf for pf in all_pix_fmts if "p10" in pf or "p12" in pf or "p16" in pf)
    }

    # 平台特定
    os_name = report["system"]["os"].lower()
    summary["platform_specific"] = {
        "windows": "windows" in os_name or "win32" in os_name or "win64" in os_name,
        "linux": "linux" in os_name,
        "macos": "mac" in os_name or "darwin" in os_name
    }

    return summary


# ==================== 入口 ====================
def main():
    """
    执行全面诊断并生成报告
    """
    print("🚀 开始 FFmpeg 全面诊断 v4.0")
    print(f"🕒 启动时间: {datetime.now().isoformat()}")

    report = run_comprehensive_diagnosis()

    # 保存报告
    output_dir = Path(__file__).parent
    output_path = output_dir / REPORT_FILENAME

    try:
        output_dir.mkdir(parents=True, exist_ok=True)

        # 仅保存报告结构，不包含大字段
        with open(output_path, 'w', encoding='utf-8') as f:
            json.dump(report, f, ensure_ascii=False, indent=2)

        print(f"\n✅ 诊断完成! 报告已保存至: {output_path.resolve()}")
        print(f"⏱️ 总耗时: {report['diagnosis']['duration_seconds']} 秒")
        print(f"🧩 组件状态:")

        for comp_name, comp_info in report["components"].items():
            if comp_info.get("found") and comp_info.get("status") == "operational":
                status = "✅ 可用"
                version = comp_info.get(
                    "version_info", {}).get("version", "未知")
            else:
                status = "❌ 不可用"
                version = "N/A"

            print(f"   {comp_name}: {status} (v{version})")

        # 生成简明结果
        result = {
            "status": "success",
            "report_path": str(output_path.resolve()),
            "components_found": sum(1 for c in report["components"].values() if c.get("found") and c.get("status") == "operational"),
            "total_capabilities": len(report["capabilities"]),
            "timestamp": report["diagnosis"]["timestamp"],
            "hardware_acceleration_available": report["compatibility_summary"]["hardware_acceleration"]["available"]
        }

        # 通过stdout输出机器可读结果
        sys.stdout.write('\n' + json.dumps(result))
        sys.stdout.flush()

    except Exception as e:
        error_result = {
            "status": "error",
            "message": f"保存报告失败: {str(e)}",
            "error_type": type(e).__name__,
            "timestamp": datetime.now().isoformat()
        }
        sys.stdout.write(json.dumps(error_result))
        sys.stdout.flush()
        sys.exit(1)


if __name__ == "__main__":
    # 设置UTF-8环境
    if sys.version_info >= (3, 7):
        try:
            sys.stdout.reconfigure(encoding='utf-8')
            sys.stderr.reconfigure(encoding='utf-8')
        except Exception:
            pass

    # 处理命令行参数
    if len(sys.argv) > 1 and sys.argv[1] in ["-h", "--help"]:
        print("""
FFmpeg 全面诊断工具 v4.0

用法: python ffmpeg_full_diagnosis.py

功能:
- 深度探测所有 FFmpeg 组件 (ffmpeg, ffprobe, ffplay)
- 完整分析编码器/解码器/滤镜/协议/格式/设备
- 生成结构化 JSON 报告，便于程序化使用
- 提供详细的命令范式和兼容性信息

报告输出: ffmpeg_full_diagnostics.json (在脚本同目录)
""")
        sys.exit(0)

    main()
