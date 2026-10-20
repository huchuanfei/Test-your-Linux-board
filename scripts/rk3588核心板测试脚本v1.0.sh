#!/bin/sh
# ============================================================
# rk3588 核心板生产测试脚本 v1.0
# 适用：嵌入式板卡生产测试工具 BoardTestTool
# 遵循项目脚本协议：stderr 输出日志，stdout 输出纯净 JSON 结果
# 兼容 busybox sh / POSIX ash，不使用 bash 专属语法
#
# 测试项：
#   1. ttyUSB0 自发自收（硬件 RXD/TXD 已短接环路）
#   2. ttyUSB1 自发自收（硬件 RXD/TXD 已短接环路）
# 单项失败只记录结果并继续，全部跑完后 stdout 打 JSON，exit 0。
# ============================================================
#
# 使用前提：
#   1. 板卡上存在 /dev/ttyUSB0、/dev/ttyUSB1
#   2. 对应口的 TX 与 RX 已硬件短接（物理回环）
#   3. 执行用户对设备有读写权限（通常需 root 或 dialout/tty 组）
#   4. 串口未被其他进程占用（如 getty、console、日志守护进程）
#
# 可通过环境变量调整：
#   TTY_DEVICE   ttyUSB0 设备路径，默认 /dev/ttyUSB0
#   TTY_DEVICE1  ttyUSB1 设备路径，默认 /dev/ttyUSB1
#   TTY_BAUD     波特率（两项共用），默认 115200
# ============================================================

set -u

DEV0="${TTY_DEVICE:-/dev/ttyUSB0}"
DEV1="${TTY_DEVICE1:-/dev/ttyUSB1}"
BAUD="${TTY_BAUD:-115200}"
READ_TIMEOUT=3
TEST_DATA="BoardTestLoopback-0123456789-ABCDEFGHIJKLMNOPQRSTUVWXYZ"
DATA_LEN=${#TEST_DATA}

# 累积各项 JSON 对象（逗号分隔），脚本结束时一次性输出数组
ITEMS_JSON=""

# ---- 日志输出到 stderr（禁止改名成 loge 等未定义命令）----
log() { echo "$@" >&2; }

# ---- JSON 字符串转义（stdin -> stdout）----
# 转义 \ 和 " ，将换行转为 \n
json_escape() {
    sed -e 's/\\/\\\\/g' -e 's/"/\\"/g' | \
        awk 'BEGIN{ORS=""} { if(NR>1) printf "\\n"; printf "%s", $0 }'
}

# ---- 记录一项结果（不写 stdout，单项失败也不退出）----
# 参数: name item result msg rawlog
record_result() {
    _esc_name="$(printf '%s' "$1" | json_escape)"
    _esc_item="$(printf '%s' "$2" | json_escape)"
    _esc_result="$3"
    _esc_msg="$(printf '%s' "$4" | json_escape)"
    _esc_raw="$(printf '%s' "$5" | json_escape)"
    _obj="$(printf '{"test_name":"%s","test_item":"%s","result":"%s","msg":"%s","raw_log":"%s"}' \
        "$_esc_name" "$_esc_item" "$_esc_result" "$_esc_msg" "$_esc_raw")"
    if [ -z "$ITEMS_JSON" ]; then
        ITEMS_JSON="$_obj"
    else
        ITEMS_JSON="$ITEMS_JSON,$_obj"
    fi
}

# ---- 对单个串口做自收自发；失败只记录结果，由调用方继续下一项 ----
# 参数: 中文名称  英文标识  设备路径
run_loopback() {
    t_name="$1"
    t_item="$2"
    t_dev="$3"

    log "==== $t_name 开始 ===="
    log "设备: $t_dev"
    log "波特率: $BAUD  8N1  无流控"
    log "测试数据($DATA_LEN 字节): $TEST_DATA"
    log "读取超时: ${READ_TIMEOUT}s"

    # 1. 设备存在性检查
    if [ ! -e "$t_dev" ]; then
        MSG="设备不存在: $t_dev（可能未枚举 USB 转串口设备）"
        log "ERROR: $MSG"
        log "可能原因: USB 转串口模块未插入 / 驱动未加载 / 设备名不匹配"
        record_result "$t_name" "$t_item" "fail" "$MSG" "$MSG"
        return 0
    fi

    if [ ! -c "$t_dev" ]; then
        MSG="节点类型错误: $t_dev 不是字符设备"
        log "ERROR: $MSG"
        record_result "$t_name" "$t_item" "fail" "$MSG" "ls -l: $(ls -l "$t_dev" 2>&1)"
        return 0
    fi

    # 2. 权限检查
    if [ ! -r "$t_dev" ] || [ ! -w "$t_dev" ]; then
        MSG="权限不足: $t_dev 不可读写"
        log "ERROR: $MSG"
        log "可能原因: 执行用户非 root 且不在 dialout/tty 组；建议 chmod 666 $t_dev 或以 root 运行"
        record_result "$t_name" "$t_item" "fail" "$MSG" "$MSG"
        return 0
    fi

    # 3. 打开设备（保持单一 fd，避免反复打开关闭导致 RX 缓冲刷新）
    if ! exec 3<>"$t_dev"; then
        MSG="无法打开设备: $t_dev"
        log "ERROR: $MSG"
        log "可能原因: 设备被其他进程占用（getty/console/日志守护）或驱动异常"
        record_result "$t_name" "$t_item" "fail" "$MSG" "$MSG"
        return 0
    fi

    # 4. 配置串口参数：8N1 raw 无流控（通过 fd 3 配置，避免重新打开）
    log "执行 stty 配置..."
    if ! stty "$BAUD" cs8 -cstopb -parenb -crtscts -ixon -ixoff raw -echo <&3 2>/dev/null; then
        exec 3>&-
        MSG="stty 配置失败: $t_dev"
        log "ERROR: $MSG"
        log "可能原因: 非 tty 设备 / 驱动不支持该波特率 / 设备已被移除"
        record_result "$t_name" "$t_item" "fail" "$MSG" "$MSG"
        return 0
    fi
    log "stty 配置成功"

    # 5. 排空 RX 缓冲（短超时读取丢弃残留数据，无数据则超时退出，正常）
    log "排空 RX 缓冲..."
    timeout 1 dd if="$t_dev" of=/dev/null bs=256 count=1 2>/dev/null

    # 6. 发送测试数据
    log "发送测试数据..."
    if ! printf '%s' "$TEST_DATA" >&3; then
        exec 3>&-
        MSG="写入 $t_dev 失败"
        log "ERROR: $MSG"
        record_result "$t_name" "$t_item" "fail" "$MSG" "$MSG"
        return 0
    fi
    log "发送完成"

    # 7. 读取回环数据（带超时，按字节精确读取 DATA_LEN 字节）
    log "读取回环数据（超时 ${READ_TIMEOUT}s）..."
    RESPONSE="$(timeout "$READ_TIMEOUT" dd bs=1 count="$DATA_LEN" <&3 2>/dev/null)"
    READ_RC=$?
    log "读取结束，退出码=$READ_RC"
    log "接收数据(${#RESPONSE} 字节): $RESPONSE"

    # 8. 关闭设备
    exec 3>&-

    # 9. 比较收发数据
    RAW_LOG="发送: $TEST_DATA | 接收: $RESPONSE | 读退出码=$READ_RC"

    if [ "$READ_RC" -eq 124 ]; then
        MSG="超时未收到完整回环数据"
        log "FAIL: $MSG"
        log "可能原因: TX/RX 未短接 / 接线松动 / 串口被其他进程占用 / 波特率不匹配"
        record_result "$t_name" "$t_item" "fail" "$MSG" "$RAW_LOG"
        return 0
    fi

    if [ "$RESPONSE" = "$TEST_DATA" ]; then
        MSG="自收自发校验成功（${DATA_LEN} 字节一致）"
        log "PASS: $MSG"
        record_result "$t_name" "$t_item" "pass" "$MSG" "$RAW_LOG"
        return 0
    fi

    MSG="收发数据不一致"
    log "FAIL: $MSG"
    log "可能原因: 部分回环 / 接线虚接 / 缓冲残留干扰 / 串口参数不匹配"
    record_result "$t_name" "$t_item" "fail" "$MSG" "$RAW_LOG"
    return 0
}

log "==== rk3588核心板测试（共 2 项）===="
log "设备0: $DEV0  设备1: $DEV1  波特率: $BAUD"

run_loopback "ttyUSB0自发自收测试" "ttyusb0_loopback" "$DEV0"
run_loopback "ttyUSB1自发自收测试" "ttyusb1_loopback" "$DEV1"

log "==== 全部测试项结束，输出 JSON ===="
# stdout 仅此一段纯净 JSON 数组（engine/parser.go 解析多项 test_item）
printf '[%s]\n' "$ITEMS_JSON"
exit 0
