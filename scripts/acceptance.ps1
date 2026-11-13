#requires -Version 7.2
[CmdletBinding()]
param(
    [string]$Go = 'F:/tmp/toolnest-build/go/bin/go.exe',
    [string]$MakeNSIS,
    [switch]$CompileInstaller
)
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot -Parent
& (Join-Path $PSScriptRoot 'test-packaging.ps1')
Push-Location $root
try {
    & $Go test -mod=readonly -count=1 ./packaging/... ./scripts/...
    if ($LASTEXITCODE) { throw 'Offline Go tests failed.' }
    if (!$CompileInstaller) {
        Write-Output 'PASS: offline checks only. No installer, uninstall, PATH, Run or task mutation performed.'
        return
    }
    if (!$MakeNSIS -or !(Test-Path -LiteralPath $MakeNSIS -PathType Leaf)) { throw 'Explicit MakeNSIS executable required.' }
    $work = Join-Path ([IO.Path]::GetTempPath()) ('toolnest-installer-acceptance-' + [guid]::NewGuid().ToString('N'))
    [void](New-Item -ItemType Directory -Path $work)
    $assets=Join-Path $work 'brand-assets'
    & (Join-Path $PSScriptRoot 'Build-BrandAssets.ps1') -Output $assets -Go $Go
    Copy-Item -LiteralPath (Join-Path $assets 'ToolNest.ico') -Destination $work
    # 维护助手是真实编译产物；主程序仅静态 fixture，从不冒充可运行应用。
    & $Go build -mod=readonly -trimpath -o (Join-Path $work 'ToolNest-maintenance.exe') ./packaging/maintenance
    if ($LASTEXITCODE) { throw 'Maintenance helper compilation failed.' }
    [IO.File]::WriteAllBytes((Join-Path $work 'ToolNest.exe'),[Text.Encoding]::ASCII.GetBytes('MZ non-runnable NSIS compile fixture'))
    [IO.File]::WriteAllText((Join-Path $work 'release-identity.json'),'{"fixture":true,"neverExecute":true}')
    $version = (Get-Content -LiteralPath (Join-Path $root 'packaging/VERSION') -Raw).Trim()
    $installer = Join-Path $work 'TEST-ONLY-DO-NOT-INSTALL.exe'
    & $MakeNSIS /V3 /INPUTCHARSET UTF8 "/DVERSION=$version" "/DPAYLOAD=$work" "/DOUTPUT=$installer" (Join-Path $root 'packaging/toolnest.nsi')
    if ($LASTEXITCODE -or !(Test-Path -LiteralPath $installer -PathType Leaf)) { throw 'NSIS compilation failed.' }
    Write-Output "PASS: compile-only installer fixture at $installer. NEVER executed."
} finally { Pop-Location }
