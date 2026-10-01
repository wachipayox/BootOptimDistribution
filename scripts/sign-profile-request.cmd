@echo off
setlocal
title Wachiland Distribution - Firmar solicitud de perfil

if "%~1"=="" (
    echo Arrastra el JSON de solicitud sobre este archivo para firmarlo.
    echo.
    pause
    exit /b 2
)

set "GIT_BASH=%ProgramFiles%\Git\bin\bash.exe"
if not exist "%GIT_BASH%" (
    echo No encuentro Git Bash en "%GIT_BASH%".
    echo Instala Git for Windows o ejecuta scripts/sign-profile-request.sh desde Bash.
    echo.
    pause
    exit /b 1
)

"%GIT_BASH%" "%~dp0sign-profile-request.sh" "%~1"
set "RESULT=%ERRORLEVEL%"
echo.
if "%RESULT%"=="0" (
    echo Firma completada.
) else (
    echo No se pudo firmar la solicitud. Revisa el mensaje anterior.
)
pause
exit /b %RESULT%
