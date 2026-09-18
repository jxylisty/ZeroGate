@echo off
rem ============================================================
rem  ZeroGate 重启脚本：结束占用端口的旧进程 -> 重新编译 -> 启动
rem  用法：双击本文件，或在终端输入 .\restart.bat
rem  端口默认 7863，如 config.json 改了端口请同步修改 _PORT
rem ============================================================
setlocal
set _PORT=7863

echo [1/3] 结束占用端口 %_PORT% 的旧进程...
for /f "tokens=5" %%p in ('netstat -ano ^| findstr :%_PORT% ^| findstr LISTENING') do (
    echo    结束 PID %%p
    taskkill /f /pid %%p >nul 2>&1
)
timeout /t 1 /nobreak >nul

echo [2/3] 重新编译...
cd /d %~dp0
go build -o daemon.exe ./cmd/wild-work
if errorlevel 1 (
    echo    编译失败，请检查上方错误
    pause
    exit /b 1
)
echo    编译成功 daemon.exe

echo [3/3] 启动...
rem ZCode 桥接：上游走本地 zcode-proxy（18080，进程内验证码求解）
set ZCODE_BRIDGE_URL=http://127.0.0.1:18080
set ZCODE_BRIDGE_KEY=sk-04cEYoXf3mThKD0El6zG9jyrfc4YgZH4
start "" "%~dp0daemon.exe" --no-tray
echo    已启动，几秒后访问 http://127.0.0.1:%_PORT%/
endlocal