@echo off
rem ENCODING: GBK(936). Do NOT re-save this file as UTF-8 - cmd.exe on this box (codepage 936) misparses UTF-8 .bat into stray | & ^ tokens.
rem ============================================================
rem  ZeroGate 重启脚本：结束旧进程 -> 重新编译 -> 启动（带托盘）
rem  用法：双击本文件，或在终端输入 .\restart.bat
rem  端口默认 7863，如 config.json 改了端口请同步修改 _PORT
rem
rem  构建参数 -H windowsgui 是必须的（勿删）：
rem    daemon 默认编译为控制台子系统，双击/开机自启会弹一个黑色 cmd 窗口，
rem    误点关闭还会连带杀掉服务。GUI 子系统 = 无窗口常驻 + 托盘图标。
rem    代价：终端里直接跑 daemon.exe 看不到输出 —— 需要看日志请用
rem    --no-tray（会自动接回父控制台）或直接看 data\app.log。
rem ============================================================
setlocal
set _PORT=7863

echo [1/3] 结束旧的 ZeroGate 进程...
rem 先按端口杀（覆盖 config 改过端口但 _PORT 没同步的情况），再按映像名兜底
for /f "tokens=5" %%p in ('netstat -ano ^| findstr :%_PORT% ^| findstr LISTENING') do (
    echo    结束 PID %%p
    taskkill /f /pid %%p >nul 2>&1
)
taskkill /f /im daemon.exe >nul 2>&1
ping 127.0.0.1 -n 2 >nul

echo [2/3] 重新编译...
cd /d %~dp0
go build -ldflags "-H windowsgui" -o daemon.exe ./cmd/zerogate
if errorlevel 1 (
    echo    编译失败，请检查上方错误
    pause
    exit /b 1
)
echo    编译成功 daemon.exe（GUI 子系统，无控制台窗口）

echo [3/3] 启动...
rem ZCode 桥接：上游走本地 zcode-proxy（默认 18083 实例，账号文件 bridgeUrl 优先）
set ZCODE_BRIDGE_URL=http://127.0.0.1:18083
set ZCODE_BRIDGE_KEY=sk-04cEYoXf3mThKD0El6zG9jyrfc4YgZH4
rem 不加 --no-tray：常驻托盘，右键菜单可打开主界面 / 查看日志 / 退出
start "" "%~dp0daemon.exe"

rem 等待监听就绪（GUI 子系统无输出可读，只能探端口）
set _UP=
for /l %%i in (1,1,15) do (
    if not defined _UP (
        ping 127.0.0.1 -n 2 >nul
        netstat -ano | findstr :%_PORT% | findstr LISTENING >nul 2>&1 && set _UP=1
    )
)
echo.
if defined _UP (
    echo   已就绪：http://127.0.0.1:%_PORT%/
    echo   托盘图标在任务栏右下角（若被 Win11 折叠，点 ^^ 展开并拖出来即可）
) else (
    echo   [警告] %_PORT% 端口未在 30 秒内监听，请查看 data\app.log 排查
)
echo.
echo 关闭本窗口不影响服务运行（daemon 已脱离控制台）
timeout /t 6 >nul
endlocal
