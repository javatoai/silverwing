@echo off
setlocal

set "SILVERWING_CLI_HOME=%~dp0.."
set "SILVERWING_JAVA=%SILVERWING_CLI_HOME%\..\runtime\bin\java.exe"

rem Installed CLI: <version>/cli/bin. Green package: app/resources/silverwing/bin.
if not exist "%SILVERWING_JAVA%" set "SILVERWING_JAVA=%SILVERWING_CLI_HOME%\..\silverwing-runtime\bin\java.exe"

if not exist "%SILVERWING_JAVA%" if defined JAVA_HOME set "SILVERWING_JAVA=%JAVA_HOME%\bin\java.exe"
if not exist "%SILVERWING_JAVA%" set "SILVERWING_JAVA=java"

"%SILVERWING_JAVA%" -cp "%SILVERWING_CLI_HOME%\lib\*" com.snowball.silverwing.cli.MainKt %*
set "SILVERWING_EXIT_CODE=%ERRORLEVEL%"
endlocal & exit /b %SILVERWING_EXIT_CODE%
