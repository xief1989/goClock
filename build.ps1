# GoClock 构建脚本
#
# 用法：
#   .\build.ps1              # 生成图标资源并编译出 GoClock.exe
#   .\build.ps1 -SkipRes     # 跳过资源生成，只编译（rsrc.syso 已存在时更快）
#
# 资源文件 rsrc.syso 由 app.manifest 与 assets\icon.ico 打包而成，
# 它提供两样东西：Common Controls 6（界面才是当前系统的样式）与可执行文件图标。

param(
    [switch]$SkipRes
)

$ErrorActionPreference = "Stop"
Set-Location $PSScriptRoot

if (-not $SkipRes) {
    Write-Host "[1/3] 生成图标 assets\icon.ico ..." -ForegroundColor Cyan
    go run ./tools/genicon
    if ($LASTEXITCODE -ne 0) { throw "图标生成失败" }

    Write-Host "[2/3] 打包资源 rsrc.syso ..." -ForegroundColor Cyan
    go run github.com/akavel/rsrc@v0.10.2 -manifest app.manifest -ico assets/icon.ico -o rsrc.syso
    if ($LASTEXITCODE -ne 0) { throw "资源打包失败（需要能访问 Go 模块代理）" }
}

Write-Host "[3/3] 编译 GoClock.exe ..." -ForegroundColor Cyan
# -H windowsgui 让程序以窗口子系统启动，不会弹出黑色控制台窗口。
go build -ldflags="-H windowsgui -s -w" -o GoClock.exe .
if ($LASTEXITCODE -ne 0) { throw "编译失败" }

$size = [math]::Round((Get-Item GoClock.exe).Length / 1MB, 2)
Write-Host "完成：GoClock.exe（$size MB）" -ForegroundColor Green
