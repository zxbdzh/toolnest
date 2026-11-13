#requires -Version 7.2
[CmdletBinding()]
param()
Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'
$root = Split-Path $PSScriptRoot -Parent
# 这些检查只读脚本与源码，不启动安装器、不操作注册表或计划任务。
$files = @('scripts/build.ps1','scripts/test-packaging.ps1','scripts/acceptance.ps1','scripts/Assert-WindowsGui.ps1','scripts/Test-WindowsGui.ps1','scripts/Read-BuildInfo.ps1','scripts/Build-BrandAssets.ps1','tests/e2e/Build-ManualUpdateKit.ps1','tests/e2e/Installed-Kit-Worker.ps1')
foreach ($file in $files) {
    $path = Join-Path $root $file
    if (!(Test-Path -LiteralPath $path -PathType Leaf)) { throw "Missing script: $file" }
    $tokens = $null; $errors = $null
    [void][System.Management.Automation.Language.Parser]::ParseFile($path,[ref]$tokens,[ref]$errors)
    if ($errors.Count) { throw ($errors | ForEach-Object Message | Out-String) }
}
$nsi = Get-Content -LiteralPath (Join-Path $root 'packaging/toolnest.nsi') -Raw
foreach ($pattern in @('RequestExecutionLevel user','SetShellVarContext current','DeleteRegKey HKCU','remove-owned-run','CreateFileW','check-install','check-uninstall')) {
    if (!$nsi.Contains($pattern)) { throw "Installer safety requirement missing: $pattern" }
}
# 只检查指令行，避免中文说明文字中的风险关键词造成假阳性。
$active = ($nsi -split "`n" | Where-Object { $_ -notmatch '^\s*;' }) -join "`n"
if ($active -match '(?im)^\s*(RMDir\s+/r|Delete\s+/REBOOTOK|WriteReg\w+\s+HKLM)' -or $active -match '(?i)taskkill|schtasks|RunOnce|CurrentVersion\\Run') { throw 'Installer contains forbidden mutation.' }
if ($active -match '(?im)^\s*WriteReg\w+.*(?:Environment|\\Run[" ])') { throw 'Installer writes PATH or autostart.' }
$version = (Get-Content -LiteralPath (Join-Path $root 'packaging/VERSION') -Raw).Trim()
if ($version -notmatch '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$') { throw 'Version invalid.' }
foreach($file in @('scripts/build.ps1','tests/e2e/Build-TestVersions.ps1')){
    $build=Get-Content -LiteralPath (Join-Path $root $file) -Raw
    if(!$build.Contains('-H=windowsgui')-or !$build.Contains('Assert-WindowsGui.ps1')){throw ('GUI build or actual PE verification is missing: '+$file)}
}
& (Join-Path $PSScriptRoot 'Test-WindowsGui.ps1')
Write-Output 'PASS: PowerShell syntax, per-user installer policy, no forced close or recursive data cleanup.'
