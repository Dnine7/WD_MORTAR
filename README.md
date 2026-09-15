# Wardogs 迫击炮坐标距离计算器

一个带桌面控制台和游戏内悬浮窗的 Windows 小工具。程序通过截图和内置离线 OCR 读取坐标，不联网、不读取游戏内存，也不注入游戏进程。

## 下载与分享

日常使用只需要一个文件：

```text
dist\WardogsMortar.exe
```

把这个 EXE 直接发给其他人即可。对方无需安装程序或 OCR 组件，也不需要管理员权限。

运行要求：

- Windows 10/11 x64。
- Microsoft Edge WebView2 Runtime（Windows 10/11 通常已自带）。
- 首次启动可能需要几秒钟，程序会把内置 OCR 引擎释放到 `%LOCALAPPDATA%\WardogsMortar\ocr`，之后启动会更快。

当前 EXE 没有商业代码签名证书，Windows SmartScreen 可能显示“Windows 已保护你的电脑”。确认文件来自可信来源后，可点击“更多信息”→“仍要运行”。分发前可用下面的命令生成校验值，并把结果一起发给对方：

```powershell
Get-FileHash .\dist\WardogsMortar.exe -Algorithm SHA256
```

## 使用方法

1. 启动程序和 Wardogs。
2. 在控制台点击“查找并绑定 Wardogs”；也可以先激活游戏，再从托盘菜单选择“绑定当前前台窗口”。
3. 根据需要读取坐标：
   - 从聊天输入框读取：在左上角当前输入行打出完整的 `x... y...`，无需发送。
   - 从地图读取：打开地图并把鼠标放在目标位置，让 `x/y` 坐标显示出来，同时保证聊天输入行没有完整坐标。
4. 使用控制台按钮，或在游戏位于前台时使用快捷键：
   - `F8`：记录自己坐标。
   - `F9`：记录目标坐标；已有自己坐标时会立即计算距离。
   - `F10`：重新计算距离。

操作提示浮窗会显示自己坐标、目标坐标、二维直线距离、OCR 来源或错误。成功记录自己、成功记录目标或重新计算后，浮窗显示 3 秒并自动隐藏；它置顶、鼠标穿透，不会抢走游戏焦点。距离按游戏坐标比例换算（1 坐标单位 = 100 米），并显示为最接近的整数米。

关闭控制台窗口后，程序会继续驻留在系统托盘；可从托盘菜单重新打开控制台或彻底退出。

## OCR 与配置

`F8` 和 `F9` 都优先读取左上角聊天当前输入行。聊天输入没有完整坐标时，才会读取地图区域。为避免一次识别抖动误取小地图，聊天区域首次失败后会短暂重试一次。只有同时读到完整 X/Y 时才更新记录；识别失败时会保留上一次有效坐标。

坐标解析支持大小写、空格、逗号、小数点、Y/X 颠倒，以及常见的 `O→0`、`I/l→1` OCR 误识别。

默认区域针对 3440×1440 截图，程序会按游戏所在显示器的当前分辨率等比例缩放。如果界面比例或 HUD 位置不同，可在控制台底部展开“OCR 识别区域”并修改。四个区域数值依次是参考分辨率下的 `x/y/w/h`。同一设置面板也可调整浮窗的水平和垂直位置：`0` 表示左侧/顶部，`100` 表示右侧/底部。

用户配置保存在：

```text
%LOCALAPPDATA%\WardogsMortar\config.json
```

如需重置程序，退出后删除 `%LOCALAPPDATA%\WardogsMortar` 即可；下次启动时会重新生成配置并释放 OCR 文件。

## 开发与构建

项目使用 Go、Wails v2 和原生 Win32，要求 Go 1.25 或更高版本。

构建便携版单文件 EXE：

```powershell
go test ./...
.\scripts\build-exe.ps1
```

输出文件为 `dist\WardogsMortar.exe`。内置 RapidOCR 引擎及英文/数字模型会一并打入 EXE。

请优先使用构建脚本，不要直接运行普通的 `go build`。Wails 的正式桌面运行时需要 `production` 构建标签；如果必须手动构建，等价命令为：

```powershell
go build -tags production -trimpath -ldflags '-s -w -H=windowsgui' -o .\dist\WardogsMortar.exe .\cmd\wardogs-mortar
```

如需使用 Wails 开发模式调试控制台：

```powershell
go install github.com/wailsapp/wails/v2/cmd/wails@v2.15.0
Set-Location .\cmd\wardogs-mortar
wails dev
```

## 项目结构

- `cmd/wardogs-mortar`：快捷键、托盘、状态流程和悬浮窗。
- `cmd/wardogs-mortar/frontend`：Wails 桌面控制台前端。
- `internal/ocr`：内置的便携式 RapidOCR 提供器。
- `internal/coords`：容错坐标解析。
- `internal/win32`：截图、窗口、热键和托盘所需的 Win32 封装。
- `scripts`：便携版 EXE 构建脚本。

第一版只计算二维距离，不提供角度、装药量或弹道计算。RapidOCR 及模型的再分发说明见 `internal/ocr/THIRD_PARTY_NOTICES.md`。
