#!/usr/bin/env python3
"""
从运行中的 Mac 微信进程提取 SQLCipher 数据库密钥
用法: python3 extract_key.py
前提: 微信正在运行且已登录
"""

import subprocess
import re
import sys
import os

def get_wechat_pid():
    result = subprocess.run(['pgrep', '-x', 'WeChat'], capture_output=True, text=True)
    if result.returncode != 0:
        return None
    pids = result.stdout.strip().split('\n')
    return pids[0] if pids else None

def extract_key_via_lldb(pid):
    """通过 lldb 附加到微信进程，搜索 SQLCipher 密钥"""

    # lldb 脚本：附加进程 → 搜索内存中的密钥模式
    # Mac WeChat 4.x 使用 SQLCipher，密钥是 64 字符 hex 字符串
    lldb_commands = f"""
import lldb
import re

debugger = lldb.SBDebugger.Create()
debugger.SetAsync(False)
target = debugger.CreateTarget("")
error = lldb.SBError()
process = target.AttachToProcessWithID(debugger.GetListener(), {pid}, error)

if error.Fail():
    print(f"ATTACH_ERROR: {{error.GetCString()}}")
else:
    print("ATTACHED")

    # 搜索 sqlite3_key 函数调用或密钥存储位置
    # 方法1: 在 memory regions 中搜索 "PRAGMA key" 附近的密钥
    target_module = None
    for module in target.module_iter():
        name = module.GetFileSpec().GetFilename() or ""
        if "WeChat" == name or "xwechat" in name.lower():
            target_module = module
            break

    # 方法2: 搜索内存中的 hex key 模式 (64 hex chars)
    # SQLCipher 密钥通常以 "x'" 或 "0x" 开头存储在内存中
    thread = process.GetSelectedThread()
    frame = thread.GetSelectedFrame()

    # 获取所有内存区域
    memory_regions = process.GetMemoryRegions()
    found_keys = set()

    for i in range(memory_regions.GetSize()):
        region = lldb.SBMemoryRegionInfo()
        memory_regions.GetMemoryRegionInfo(i, region)

        if not region.IsReadable():
            continue

        begin = region.GetRegionBase()
        end = region.GetRegionEnd()
        size = end - begin

        # 只搜索合理大小的区域 (跳过超大映射)
        if size > 100 * 1024 * 1024 or size < 64:
            continue

        try:
            err = lldb.SBError()
            data = process.ReadMemory(begin, min(size, 10 * 1024 * 1024), err)
            if err.Fail() or not data:
                continue

            # 搜索 SQLCipher PRAGMA key 模式: x'<64 hex chars>'
            # 或者直接搜索 64 字符的 hex 字符串前后带引号
            text = data.decode('latin-1')

            # 模式1: PRAGMA key = "x'...'"
            for m in re.finditer(r"x'([0-9a-fA-F]{{64}})'", text):
                key = m.group(1)
                found_keys.add(key)

            # 模式2: 直接的 64 hex 字符串 (raw key)
            # 这个太宽泛，跳过

        except Exception as e:
            continue

    if found_keys:
        for key in found_keys:
            print(f"KEY_FOUND: {{key}}")
    else:
        print("KEY_NOT_FOUND_PATTERN1")

        # 方法3: 尝试搜索 sqlcipher_codec_ctx 结构体中的密钥
        # 在较新版本中，密钥可能以 raw bytes 存储
        # 搜索 "SQLite format 3" 后面跟着的密钥材料
        print("Trying alternative search...")

        for i in range(memory_regions.GetSize()):
            region = lldb.SBMemoryRegionInfo()
            memory_regions.GetMemoryRegionInfo(i, region)

            if not region.IsReadable() or not region.IsWritable():
                continue

            begin = region.GetRegionBase()
            end = region.GetRegionEnd()
            size = end - begin

            if size > 50 * 1024 * 1024 or size < 100:
                continue

            try:
                err = lldb.SBError()
                data = process.ReadMemory(begin, min(size, 5 * 1024 * 1024), err)
                if err.Fail() or not data:
                    continue

                text = data.decode('latin-1')

                # 搜索 db_storage 路径附近的内容
                for m in re.finditer(r'db_storage', text):
                    pos = m.start()
                    # 在路径附近 ±1024 字节范围内搜索可能的密钥
                    nearby = text[max(0,pos-1024):pos+1024]
                    for km in re.finditer(r'([0-9a-fA-F]{{64}})', nearby):
                        candidate = km.group(1)
                        # 过滤掉全0或明显不是密钥的
                        if candidate != '0' * 64 and len(set(candidate)) > 4:
                            found_keys.add(candidate)
                            print(f"KEY_CANDIDATE: {{candidate}}")

            except Exception:
                continue

        if not found_keys:
            print("NO_KEYS_FOUND")

    # Detach 不终止进程
    process.Detach()
    print("DETACHED")

lldb.SBDebugger.Destroy(debugger)
"""

    # 写入临时脚本
    script_path = '/tmp/wechat_key_extract.py'
    with open(script_path, 'w') as f:
        f.write(lldb_commands)

    print(f"正在附加到微信进程 (PID: {pid})...")
    print("如果弹出密码框，请输入 Mac 登录密码并点击允许")
    print()

    result = subprocess.run(
        ['python3', '-c', f'''
import lldb
exec(open("{script_path}").read())
'''],
        capture_output=True, text=True, timeout=120
    )

    output = result.stdout + result.stderr
    print("lldb output:", output[:2000])

    # 提取密钥
    keys = []
    for line in output.split('\n'):
        if 'KEY_FOUND:' in line or 'KEY_CANDIDATE:' in line:
            key = line.split(': ', 1)[-1].strip()
            keys.append(key)

    return keys

def try_decrypt_with_key(db_path, key):
    """尝试用密钥解密数据库"""
    try:
        # 使用 sqlcipher 命令行工具测试
        result = subprocess.run(
            ['sqlcipher', db_path,
             f"PRAGMA key = \"x'{key}'\";",
             "SELECT count(*) FROM sqlite_master;"],
            capture_output=True, text=True, timeout=10
        )
        if result.returncode == 0 and result.stdout.strip():
            return True
    except (subprocess.TimeoutExpired, FileNotFoundError):
        pass
    return False

def main():
    pid = get_wechat_pid()
    if not pid:
        print("错误: 微信未在运行，请先打开微信")
        sys.exit(1)

    print(f"找到微信进程: PID {pid}")

    keys = extract_key_via_lldb(pid)

    if keys:
        print(f"\n找到 {len(keys)} 个候选密钥:")
        for i, key in enumerate(keys):
            print(f"  [{i+1}] {key}")

        # 保存到文件
        key_file = os.path.join(os.path.dirname(os.path.abspath(__file__)), '..', 'data', 'db_key.txt')
        os.makedirs(os.path.dirname(key_file), exist_ok=True)
        with open(key_file, 'w') as f:
            for key in keys:
                f.write(key + '\n')
        print(f"\n密钥已保存到: {key_file}")
    else:
        print("\n未找到密钥，请尝试以下方法:")
        print("1. 确保微信已登录且打开了聊天窗口")
        print("2. 确保已授权 lldb 调试权限")
        print("3. 可能需要关闭 SIP (System Integrity Protection)")

if __name__ == '__main__':
    main()
