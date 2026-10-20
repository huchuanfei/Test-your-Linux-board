#   本项目意在提供一种思路，即使用大模型生成Linux板卡的测试脚本、测试代码，实现小批量Linux板卡生产过程中的单板测试作业，本项目使用的大模型是GLM-5.3、DeepSeek-V4.1 flash，你可以根据你的实际需要继续完善、优化。

# Test your Linux board

基于 **Go + Fyne v2**，你可以在不同的操作系统上使用go编译器对代码进行编译发布，可以发布成不同CPU架构和操作系统的可执行文件。



---

## 功能特性

### 1. 测试主页

- 顶部板卡配置区：SSH `IP / 端口 / 用户名 / 密码`
- 【打开脚本】：直接打开本地 `.sh` 测试脚本（不提供粘贴 / 编辑，板卡差异由脚本本身区分，无需选型号）
- 【连接】/【断开】：验证 SSH 连通性并建立共享连接，按钮文案随连接状态自动切换
- 【开始测试】：远程执行脚本，自动解析结果并回填
- 左侧 **原始日志区（stderr）**：实时滚动显示脚本全部原始输出；**支持鼠标拖选、`Ctrl+A` / `Ctrl+C` 复制**（只读，不会被误改），可一键清空
- 右侧 **结果表格**：列为`序号（自动生成）/ 测试名称 / 测试ID / 结果 / 简要信息`；**未通过项仅"结果"列红字**，其余列黑字，标题旁实时显示不合格项数量（0 绿、>0 红）

### 2. SSH 终端

面向研发 / 产线的人工调试，提供接近 XShell / PuTTY 的终端体验（真正的终端仿真，而非"输出框 + 输入框"）：

- 独立配置栏（IP / 端口 / 用户名 / 密码）与【连接】【断开】【清屏】
- 连接状态指示：已连接（绿）/ 已断开（灰）/ 连接中（黄）
- 请求远端 PTY 交互式 Shell，出现真实提示符
- 键盘全量直通：普通字符、`Backspace`、`Enter`、`Tab`、方向键、`Ctrl+C` / `Ctrl+D` / `Ctrl+Z` / `Ctrl+L` 等组合键
- ANSI 转义序列解析与渲染：8 / 16 / 256 色、粗体、光标移动、清屏等（`ls --color`、`top` 等可正常显示）
- 回滚缓冲区（最多 5000 行），滚轮 / 滚动条查看历史，新输出自动跟随底部
- 鼠标拖选复制、粘贴；窗口缩放时同步通知远端 PTY 尺寸（避免全屏程序错乱）
- 网络断开 / 远端 `exit` 时显示明确提示，不闪退，可重新【连接】

### 3. 文件传输

- SFTP 在主机 ↔ 目标板之间传输，**本机路径与远端路径都支持填"文件"或"文件夹"**
- 提供【浏览文件】【浏览文件夹】两个入口
- 传文件夹时递归处理（本地 `filepath.Walk` / 远端目录树遍历），自动创建目标目录，合并汇报累计进度
- 传单个文件时目标路径即该文件的完整路径
- 实时显示已传输字节数 / 总量

---

## 板卡端脚本协议（关键约定）

测试脚本由外部大模型提前生成，放在板卡上执行。为保证客户端能稳定解析，脚本必须遵守：

1. **输出分流**
   - 所有日志、调试过程、命令输出 → 全部写入 **stderr**（`echo "..." >&2`）
   - **stdout 只输出一段纯净 JSON 数组**，无多余字符、无换行、无日志
2. **JSON 结构**

   ```json
   [
     {
       "test_name": "UART1 自发自收测试",
       "test_item": "uart1",
       "result": "pass",
       "msg": "收发100字节校验成功",
       "raw_log": "详细底层输出日志"
     }
   ]
   ```

3. **约束**：兼容 busybox `sh`（不用 bash 专属语法）；单项失败**不退出脚本**，跑完所有项；`result` 只允许 `pass` / `fail`；禁止 ANSI 颜色码

客户端对 JSON 做了容错处理：自动截取合法数组、兼容单对象输出、兼容尾部多余逗号、结果值统一小写。

`scripts/` 目录内置示例测试脚本与生成提示词模板：
- `rk3588核心板测试脚本v1.0.sh`、`3588-ds-chat.sh`
- `测试脚本生成提示词模板.md`、`rk3588核心板测试脚本提示词.md`

---

## 执行引擎（双模式自动兼容）

点击【开始测试】后，客户端优先 **SFTP 上传脚本到板卡 `/tmp` 执行**；若板卡无 SFTP 环境，自动回退为**管道直接灌入脚本执行（不落地文件）**，兼容精简嵌入式系统。

- SSH 连接超时 10s，脚本执行超时 10min（可配），异常弹窗提示、不闪退
- stdout 为 JSON 结果，stderr 为用户日志

---

## 构建

> 环境要求：Go 1.27+（见 `go.mod`）；Fyne 依赖 CGO。Linux 需要 OpenGL / X11 / Wayland 的开发头文件；Windows 需要 mingw-w64 工具链。

### Ubuntu / Linux

```bash
CGO_ENABLED=1 go build -trimpath -ldflags "-s -w" -o dist/BoardTestTool-linux-amd64 .
```

Linux 构建需安装（或本地解压 deb 后指定 `PKG_CONFIG_PATH` / `CGO_CFLAGS` / `CGO_LDFLAGS`）：

```text
libgl1-mesa-dev libx11-dev libxrandr-dev libxcursor-dev libxinerama-dev
libxi-dev libxxf86vm-dev libwayland-dev libxkbcommon-dev
```

若环境没有 Wayland 头文件，可追加 `-tags "x11"` 以 X11 模式构建。

### Windows

```bash
CGO_ENABLED=1 GOOS=windows GOARCH=amd64 go build -ldflags "-H windowsgui -s -w" -o dist/BoardTestTool-windows-amd64.exe .
```

> 运行环境：需图形桌面（标准 GL / X11 运行库），不支持纯命令行服务器。

---

## 测试

```bash
go test ./...
```

覆盖：SSH 连接与执行引擎、JSON 容错解析、文件 / 目录传输（内置测试用 SFTP 服务）、终端输出与 UTF-8 多字节处理、UI 交互逻辑。

---

## 项目结构

```text
.
├── main.go                       # 程序入口：主题、窗口、三页签装配
├── engine/                       # 核心业务
│   ├── ssh.go                    # SSH 配置与连接（密码认证）
│   ├── hub.go                    # 跨页面共享的 SSH 连接持有者
│   ├── runner.go                 # 双模式脚本执行引擎（SFTP / 管道）
│   ├── parser.go                 # 测试结果 JSON 容错解析
│   └── xfer.go                   # SFTP 单文件 / 目录递归传输
├── ui/                           # Fyne 界面
│   ├── mainpage.go               # 测试主页（配置区 + 日志 + 结果表）
│   ├── termpage.go               # SSH 终端页
│   ├── xferpage.go               # 文件传输页
│   ├── buttons.go                # 统一绿色按钮与尺寸工具
│   └── titlebar.go               # 窗口标题
├── internal/term/                # 自研终端仿真组件（VT 解析、颜色、选区、回滚）
├── cmd/termdemo/                 # 终端组件独立演示程序
├── scripts/                      # 示例板卡测试脚本与提示词模板
└── dist/                         # 构建产物（Linux / Windows）
```

---

## 许可

本项目未附带开源许可证；如需开源请自行补充 `LICENSE`。
