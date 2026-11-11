@echo off
chcp 65001 >nul
title ProxyPilot VPN ??
powershell -NoProfile -ExecutionPolicy Bypass -File "%~dp0uninstall.ps1"
echo.
pause
