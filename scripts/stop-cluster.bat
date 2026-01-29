@echo off
REM Helios Cluster Shutdown Script for Windows

echo Stopping Helios Cluster...

REM Kill all helios-atlasd processes
taskkill /F /IM helios-atlasd.exe 2>nul

if %ERRORLEVEL% EQU 0 (
    echo Cluster stopped successfully!
) else (
    echo No running processes found
)

echo Done!
pause
