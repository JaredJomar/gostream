@echo off
setlocal EnableExtensions EnableDelayedExpansion

set "SCRIPT_DIR=%~dp0"
pushd "%SCRIPT_DIR%" >nul

set "PROFILE=plex"
set "NO_CACHE=0"
set "DO_DOWN=0"
set "DO_PULL=0"
set "EXTRA_BUILD_ARGS="

:parse_args
if "%~1"=="" goto args_done

if /I "%~1"=="plex" (
  set "PROFILE=plex"
  shift
  goto parse_args
)
if /I "%~1"=="dockge" (
  set "PROFILE=dockge"
  shift
  goto parse_args
)
if /I "%~1"=="jellyfin" (
  set "PROFILE=jellyfin"
  shift
  goto parse_args
)
if /I "%~1"=="jellyfin-win" (
  set "PROFILE=jellyfin-win"
  shift
  goto parse_args
)
if /I "%~1"=="--no-cache" (
  set "NO_CACHE=1"
  shift
  goto parse_args
)
if /I "%~1"=="--down" (
  set "DO_DOWN=1"
  shift
  goto parse_args
)
if /I "%~1"=="--pull" (
  set "DO_PULL=1"
  shift
  goto parse_args
)
if /I "%~1"=="--help" goto usage
if /I "%~1"=="-h" goto usage

echo [ERROR] Unknown option: %~1
goto usage

:args_done
if defined DOCKGE_STACK_DIR (
  set "DOCKGE_STACK_DIR=%DOCKGE_STACK_DIR%"
) else (
  set "DOCKGE_STACK_DIR=%USERPROFILE%\Documents\Docker Stuff\Dockge\stacks\gostream"
)
set "COMPOSE_FILE=deploy/docker/compose.plex-gostream.yml"
set "SERVICE=plex_gostream"
set "ENV_FILE=deploy/docker/.env.plex-gostream"
set "PROJECT_DIR_ARG="

if /I "%PROFILE%"=="jellyfin" (
  set "COMPOSE_FILE=deploy/docker/compose.jellyfin-gostream.windows.yml"
  set "SERVICE=jellyfin_gostream"
  set "ENV_FILE=deploy/docker/.env.jellyfin-gostream"
)
if /I "%PROFILE%"=="jellyfin-win" (
  set "COMPOSE_FILE=deploy/docker/compose.jellyfin-gostream.windows.yml"
  set "SERVICE=jellyfin_gostream"
  set "ENV_FILE=deploy/docker/.env.jellyfin-gostream"
)
if /I "%PROFILE%"=="dockge" (
  set "ENV_FILE="
  set "PROJECT_DIR_ARG=--project-directory "%DOCKGE_STACK_DIR%""
  set "SERVICE=plex_gostream"
  if exist "%DOCKGE_STACK_DIR%\compose.yml" set "COMPOSE_FILE=%DOCKGE_STACK_DIR%\compose.yml"
  if exist "%DOCKGE_STACK_DIR%\compose.yaml" set "COMPOSE_FILE=%DOCKGE_STACK_DIR%\compose.yaml"
  if exist "%DOCKGE_STACK_DIR%\docker-compose.yml" set "COMPOSE_FILE=%DOCKGE_STACK_DIR%\docker-compose.yml"
  if exist "%DOCKGE_STACK_DIR%\docker-compose.yaml" set "COMPOSE_FILE=%DOCKGE_STACK_DIR%\docker-compose.yaml"
)

if not exist "%COMPOSE_FILE%" (
  echo [ERROR] Compose file not found: %COMPOSE_FILE%
  popd >nul
  exit /b 1
)

set "COMPOSE_ENV_ARG="
if exist "%ENV_FILE%" (
  set "COMPOSE_ENV_ARG=--env-file %ENV_FILE%"
) else (
  echo [WARN] Env file not found: %ENV_FILE%
  echo [WARN] Continuing without --env-file.
)

if "%NO_CACHE%"=="1" set "EXTRA_BUILD_ARGS=!EXTRA_BUILD_ARGS! --no-cache"
if "%DO_PULL%"=="1" set "EXTRA_BUILD_ARGS=!EXTRA_BUILD_ARGS! --pull"

echo.
echo [INFO] Profile: %PROFILE%
echo [INFO] Compose: %COMPOSE_FILE%
echo [INFO] Service: %SERVICE%
if /I "%PROFILE%"=="dockge" echo [INFO] Dockge stack dir: %DOCKGE_STACK_DIR%
echo.

set "DOCKER_BUILDKIT=1"
set "COMPOSE_DOCKER_CLI_BUILD=1"

if "%DO_DOWN%"=="1" (
  echo [STEP] Stopping stack first...
  docker compose -f "%COMPOSE_FILE%" %PROJECT_DIR_ARG% %COMPOSE_ENV_ARG% down
  if errorlevel 1 goto fail
)

echo [STEP] Building image...
docker compose -f "%COMPOSE_FILE%" %PROJECT_DIR_ARG% %COMPOSE_ENV_ARG% build %EXTRA_BUILD_ARGS% %SERVICE%
if errorlevel 1 goto fail

echo [STEP] Recreating service...
docker compose -f "%COMPOSE_FILE%" %PROJECT_DIR_ARG% %COMPOSE_ENV_ARG% up -d --no-deps %SERVICE%
if errorlevel 1 goto fail

echo [STEP] Restarting service after rebuild...
docker compose -f "%COMPOSE_FILE%" %PROJECT_DIR_ARG% %COMPOSE_ENV_ARG% restart %SERVICE%
if errorlevel 1 goto fail

echo.
echo [OK] Rebuild complete.
echo [TIP] Tail logs: docker compose -f "%COMPOSE_FILE%" %PROJECT_DIR_ARG% %COMPOSE_ENV_ARG% logs -f %SERVICE%
popd >nul
exit /b 0

:fail
echo.
echo [ERROR] Docker command failed.
popd >nul
exit /b 1

:usage
echo.
echo Usage: rebuild-docker.bat [plex^|dockge^|jellyfin^|jellyfin-win] [--no-cache] [--pull] [--down]
echo.
echo Examples:
echo   rebuild-docker.bat
echo   rebuild-docker.bat plex
echo   rebuild-docker.bat dockge
echo   set DOCKGE_STACK_DIR=D:\Docker\Dockge\stacks\gostream ^&^& rebuild-docker.bat dockge
echo   rebuild-docker.bat jellyfin --pull
echo   rebuild-docker.bat plex --no-cache --down
echo.
popd >nul
exit /b 1
