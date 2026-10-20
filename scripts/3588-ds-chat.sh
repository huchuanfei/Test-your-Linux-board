#!/bin/sh

log() { echo "$@" >&2; }

json_escape() {
    sed -e 's/\\/\\\\/g' -e 's/"/\\"/g' | \
        awk 'BEGIN{ORS=""} { if(NR>1) printf "\\n"; printf "%s", $0 }'
}

ITEMS_JSON=""
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

TMP_ERR="/tmp/boardtest_$$.err"
if ! : > "$TMP_ERR" 2>/dev/null; then
    TMP_ERR="/dev/null"
fi

for DEV in /dev/ttyUSB0 /dev/ttyUSB1; do
    case "$DEV" in
        /dev/ttyUSB0)
            NAME="ttyUSB0自发自收测试"
            ITEM="ttyusb0_loopback"
            ;;
        /dev/ttyUSB1)
            NAME="ttyUSB1自发自收测试"
            ITEM="ttyusb1_loopback"
            ;;
        *)
            continue
            ;;
    esac

    TX_DATA="ABC"
    TX_LEN=3

    if [ ! -e "$DEV" ]; then
        record_result "$NAME" "$ITEM" "fail" "设备不存在: $DEV" "无此节点"
        continue
    fi
    if [ ! -r "$DEV" ] || [ ! -w "$DEV" ]; then
        record_result "$NAME" "$ITEM" "fail" "权限不足: $DEV" "设备不可读写"
        continue
    fi

    if ! exec 3<>"$DEV" 2>/dev/null; then
        record_result "$NAME" "$ITEM" "fail" "打不开: $DEV" "打开设备失败"
        continue
    fi

    STTY_OUT="$(stty 115200 cs8 -cstopb -parenb -crtscts -ixon -ixoff raw -echo <&3 2>&1)"
    STTY_RC=$?
    if [ "$STTY_RC" -ne 0 ]; then
        record_result "$NAME" "$ITEM" "fail" "stty 配置失败: $DEV" "stty 输出: $STTY_OUT | 退出码: $STTY_RC"
        exec 3>&-
        continue
    fi

    if ! command -v timeout >/dev/null 2>&1; then
        record_result "$NAME" "$ITEM" "fail" "命令不存在: timeout" "stty 输出: $STTY_OUT"
        exec 3>&-
        continue
    fi

    printf '%s' "$TX_DATA" >&3

    : > "$TMP_ERR"
    RX="$(timeout 2 dd bs=1 count=$TX_LEN <&3 2>"$TMP_ERR")"
    DD_RC=$?
    DD_ERR="$(cat "$TMP_ERR" 2>/dev/null)"

    if [ "$DD_RC" -ne 0 ]; then
        record_result "$NAME" "$ITEM" "fail" "超时或读取失败，退出码: $DD_RC" "stty 输出: $STTY_OUT | 发送: $TX_DATA | 接收: $RX | dd错误: $DD_ERR"
    elif [ "$RX" = "$TX_DATA" ]; then
        record_result "$NAME" "$ITEM" "pass" "收发一致" "stty 输出: $STTY_OUT | 发送: $TX_DATA | 接收: $RX | dd错误: $DD_ERR"
    else
        record_result "$NAME" "$ITEM" "fail" "数据不一致" "stty 输出: $STTY_OUT | 发送: $TX_DATA | 接收: $RX | dd错误: $DD_ERR"
    fi

    exec 3>&-
done

SPI_DEV="/dev/spidev1.0"
SPI_NAME="/dev/spidev1.0读写测试"
SPI_ITEM="spidev1_0_rw"

if [ ! -e "$SPI_DEV" ]; then
    record_result "$SPI_NAME" "$SPI_ITEM" "fail" "设备不存在: $SPI_DEV" "无此节点"
elif [ ! -r "$SPI_DEV" ] || [ ! -w "$SPI_DEV" ]; then
    record_result "$SPI_NAME" "$SPI_ITEM" "fail" "权限不足: $SPI_DEV" "设备不可读写"
elif ! command -v timeout >/dev/null 2>&1; then
    record_result "$SPI_NAME" "$SPI_ITEM" "fail" "命令不存在: timeout" "无法执行带超时的spidev读写"
else
    : > "$TMP_ERR"
    printf '\126' | timeout 2 dd of="$SPI_DEV" bs=1 count=1 2>"$TMP_ERR"
    WR_RC=$?
    WR_ERR="$(cat "$TMP_ERR" 2>/dev/null)"

    if [ "$WR_RC" -ne 0 ]; then
        record_result "$SPI_NAME" "$SPI_ITEM" "fail" "写入失败: $SPI_DEV" "写入0x56退出码: $WR_RC | dd错误: $WR_ERR"
    else
        : > "$TMP_ERR"
        RX="$(timeout 2 dd if="$SPI_DEV" bs=1 count=1 2>"$TMP_ERR")"
        RD_RC=$?
        RD_ERR="$(cat "$TMP_ERR" 2>/dev/null)"

        if [ "$RD_RC" -ne 0 ]; then
            record_result "$SPI_NAME" "$SPI_ITEM" "fail" "回读失败，退出码: $RD_RC" "写入0x56成功 | 回读dd错误: $RD_ERR"
        elif [ "$RX" = "V" ]; then
            record_result "$SPI_NAME" "$SPI_ITEM" "pass" "写入0x56回读正确" "地址0写入: 0x56 | 回读: 0x56(V) | 写入dd错误: $WR_ERR | 回读dd错误: $RD_ERR"
        else
            RAW_RX="$(printf '%s' "$RX" | sed -n l)"
            record_result "$SPI_NAME" "$SPI_ITEM" "fail" "回读数据不一致" "地址0写入: 0x56 | 回读原始: $RAW_RX | 写入dd错误: $WR_ERR | 回读dd错误: $RD_ERR"
        fi
    fi
fi

printf '[%s]\n' "$ITEMS_JSON"
exit 0
